<script lang="ts">
	import { afterNavigate, goto } from '$app/navigation';
	import { page } from '$app/state';
	import {
		cancelRequest,
		executeAction,
		getActions,
		getMediaEvents,
		getRemovalPlan,
		getRequestDetail,
		isNotFound,
		removeTitle,
		retryItem
	} from '$lib/api';
	import { clock } from '$lib/clock.svelte';
	import RemovalPlanView from '$lib/components/RemovalPlanView.svelte';
	import StageBadge from '$lib/components/StageBadge.svelte';
	import StageStrip from '$lib/components/StageStrip.svelte';
	import StageTimeline from '$lib/components/StageTimeline.svelte';
	import { confirm } from '$lib/confirm.svelte';
	import { live } from '$lib/live.svelte';
	import type { Action, ItemDetail, RawEvent, RemovalPlan, RequestDetail } from '$lib/types';
	import { cn, formatBytesOrDash, formatTime, relativeTime } from '$lib/utils';
	import { MediaQuery } from 'svelte/reactivity';

	const id = $derived(Number(page.params.id));

	let detail = $state<RequestDetail | null>(null);
	let requestActions = $state<Action[]>([]);
	const seasonActions = $derived(requestActions.filter((a) => a.kind === 'season-search'));
	const resendAction = $derived(requestActions.find((a) => a.kind === 'resend-notify'));

	let selected = $state<ItemDetail | null>(null);
	let rawEvents = $state<RawEvent[] | null>(null);
	let rawError = $state(false);
	let notFound = $state(false);
	let loadError = $state(false);
	let busy = $state(false);

	// One line of feedback above the page: neutral for "done", red for failures.
	type Flash = { text: string; tone: 'info' | 'error' };
	let flash = $state<Flash | null>(null);
	const info = (text: string): Flash => ({ text, tone: 'info' });
	const fail = (text: string): Flash => ({ text, tone: 'error' });

	// Back goes where the user came from (a filtered Flow, History), not
	// always to the plain Flow.
	let back = $state({ href: '/', label: 'Flow' });
	afterNavigate(({ from }) => {
		if (from?.url && from.url.pathname === '/') {
			const done = from.url.searchParams.get('status') === 'done';
			back = { href: from.url.pathname + from.url.search, label: done ? 'History' : 'Flow' };
		}
	});

	// Wide screens show the timeline next to the list; narrow ones right under
	// the tapped row, so tapping a row visibly does something.
	const wide = new MediaQuery('min-width: 1280px');

	$effect(() => {
		void live.pipelineTick;
		void loadDetail(id);
	});

	// Reset page state when navigating between requests.
	$effect(() => {
		void id;
		notFound = false;
		loadError = false;
		flash = null;
		detail = null;
		selected = null;
		rawEvents = null;
		requestActions = [];
	});

	async function loadDetail(reqID: number) {
		if (!Number.isFinite(reqID)) return;
		try {
			const result = await getRequestDetail(reqID);
			if (reqID !== id) return; // stale response after navigation
			detail = result;
			notFound = false;
			loadError = false;
			try {
				const acts = await getActions('request', reqID);
				if (reqID === id) requestActions = acts; // re-check: response may be stale
			} catch {
				if (reqID === id) requestActions = [];
			}
			if (selected) {
				selected = detail.items.find((i) => i.id === selected!.id) ?? null;
			}
			if (selected === null && detail.items.length === 1) {
				selected = detail.items[0];
			}
		} catch (e) {
			if (reqID !== id) return;
			if (isNotFound(e)) notFound = true;
			else loadError = true;
		}
	}

	async function doCancel() {
		if (!detail) return;
		const ok = await confirm.ask({
			title: 'Cancel request',
			message: `Stop "${detail.request.title}"? In-flight downloads are cancelled and the Seerr request is removed. The series/movie itself stays in Sonarr/Radarr.`,
			confirmLabel: 'Cancel request',
			danger: true
		});
		if (!ok) return;
		busy = true;
		flash = null;
		try {
			await cancelRequest(id);
			flash = info('Request canceled');
			await loadDetail(id);
		} catch {
			flash = fail('Cancel failed');
		} finally {
			busy = false;
		}
	}

	// Remove everywhere: preview first, then delete the series/movie in
	// Sonarr/Radarr (with files), Seerr, Jellyfin, the download client and here.
	// The plan hash goes back with the delete, so the server refuses when the
	// plan changed between preview and confirm; then the fresh plan is shown.
	let removalPlan = $state<RemovalPlan | null>(null);

	async function doRemove(notice?: string) {
		if (!detail) return;
		busy = true;
		flash = null;
		try {
			removalPlan = await getRemovalPlan(id);
		} catch {
			flash = fail('Could not prepare the removal');
			busy = false;
			return;
		}
		busy = false;
		const plan = removalPlan;
		const watching = plan.viewers.length > 0;
		const base = `Delete "${plan.title}" from every system? Works whether it is still downloading or long finished.`;
		const ok = await confirm.ask({
			title: watching ? 'Someone is watching this' : 'Remove everywhere',
			message: notice ? `${notice}. ${base}` : base,
			body: planBody,
			confirmLabel: watching ? 'Remove anyway' : 'Remove everywhere',
			danger: true
		});
		if (!ok) return;
		busy = true;
		try {
			const res = await removeTitle(id, watching, plan.plan_hash);
			if (res.ok) {
				await goto(back.href);
				return;
			}
			if (res.conflict === 'plan_changed') {
				busy = false;
				await doRemove('The removal plan changed — review it again');
				return;
			}
			flash =
				res.conflict === 'watching'
					? fail('Someone started watching it — nothing was removed. Try again.')
					: fail(`Removal stopped: ${res.error ?? 'unknown error'}`);
			await loadDetail(id);
		} catch {
			flash = fail('Removal failed');
		} finally {
			busy = false;
		}
	}

	async function runSeason(a: Action) {
		busy = true;
		flash = null;
		try {
			await executeAction('season-search', { request_id: a.request_id, season: a.season });
			flash = info(`Searching season ${a.season}`);
			await loadDetail(id);
		} catch {
			flash = fail(`Season ${a.season} search failed`);
		} finally {
			busy = false;
		}
	}

	async function doRetry(item: ItemDetail) {
		const ok = await confirm.ask({
			title: 'Retry download',
			message: `Blocklist the current release for "${item.title || epLabel(item)}" and search again?`,
			confirmLabel: 'Retry'
		});
		if (!ok) return;
		busy = true;
		flash = null;
		try {
			await retryItem(item.id);
			flash = info('Retry started');
			await loadDetail(id);
		} catch {
			flash = fail('Retry failed');
		} finally {
			busy = false;
		}
	}

	async function doResend() {
		if (!resendAction) return;
		busy = true;
		flash = null;
		try {
			await executeAction('resend-notify', { request_id: resendAction.request_id });
			flash = info('Notification queued');
			await loadDetail(id);
		} catch {
			flash = fail('Resend failed');
		} finally {
			busy = false;
		}
	}

	function select(item: ItemDetail) {
		selected = selected?.id === item.id ? null : item;
		rawEvents = null;
		rawError = false;
	}

	async function loadRaw() {
		if (!selected) return;
		rawError = false;
		try {
			rawEvents = await getMediaEvents(selected.id);
		} catch {
			rawError = true;
		}
	}

	function epLabel(item: ItemDetail): string {
		if (item.media_type === 'movie') return 'Movie';
		const s = String(item.season_number ?? 0).padStart(2, '0');
		const e = String(item.episode_number ?? 0).padStart(2, '0');
		return `S${s}E${e}`;
	}

	const dlStateColor: Record<string, string> = {
		grabbed: 'text-info',
		imported: 'text-success',
		failed: 'text-destructive',
		canceled: 'text-muted-foreground'
	};
	const btn = 'tap shrink-0 rounded-md px-2.5 py-1 text-xs font-medium disabled:opacity-50';
</script>

{#snippet planBody()}
	{#if removalPlan}<RemovalPlanView plan={removalPlan} />{/if}
{/snippet}

{#snippet timeline(item: ItemDetail)}
	{#if item.current_stage !== 'available' && item.current_stage !== 'notified'}
		<div class="mb-3 flex justify-end">
			<button onclick={() => doRetry(item)} disabled={busy} class={cn(btn, 'border border-border hover:bg-accent')}>
				Retry
			</button>
		</div>
	{/if}
	{#if item.stuck_since}
		<p class="mb-3 rounded-md bg-warning/10 px-2.5 py-1.5 text-[11px] text-warning">
			⏳ Stuck — no progress since {relativeTime(item.stuck_since, clock.now)}
		</p>
	{/if}
	{#if item.last_error}
		<p class="mb-3 break-all rounded-md bg-destructive/10 px-2.5 py-1.5 text-[11px] text-destructive">
			{item.last_error}
		</p>
	{/if}
	<StageTimeline transitions={item.transitions} currentCycle={item.current_cycle} currentStage={item.current_stage} />
	{#if item.imported_path}
		<p class="mt-2 break-all border-t border-border pt-2 text-[11px] text-muted-foreground">
			{item.imported_path}
		</p>
	{/if}
	<button
		onclick={loadRaw}
		class="tap mt-3 text-[11px] text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
	>
		{rawEvents ? 'reload raw events' : 'show raw events'}
	</button>
	{#if rawError}
		<span class="ml-2 text-[11px] text-destructive">failed to load</span>
	{/if}
	{#if rawEvents}
		<div class="mt-2 max-h-80 space-y-1.5 overflow-y-auto">
			{#each rawEvents as ev (ev.id)}
				<details class="rounded-md bg-muted/50 px-2 py-1.5 text-[11px]">
					<summary class="cursor-pointer text-muted-foreground">
						<span class="font-medium text-foreground">{ev.source}/{ev.kind}</span>
						· {formatTime(ev.received_at)}
					</summary>
					<pre class="mt-1 overflow-x-auto text-[10px] leading-relaxed">{JSON.stringify(ev.payload, null, 2)}</pre>
				</details>
			{/each}
		</div>
	{/if}
{/snippet}

{#if notFound}
	<p class="text-sm text-muted-foreground">Request not found. <a href={back.href} class="underline">Back to {back.label}</a></p>
{:else if loadError}
	<div class="text-sm text-muted-foreground">
		Couldn't load this request.
		<button class="underline" onclick={() => loadDetail(id)}>Retry</button>
		· <a href={back.href} class="underline">Back to {back.label}</a>
	</div>
{:else if detail}
	{#if flash}
		<div
			role={flash.tone === 'error' ? 'alert' : 'status'}
			class={cn(
				'mb-3 rounded-md px-3 py-1.5 text-xs',
				flash.tone === 'error' ? 'bg-destructive/10 text-destructive' : 'bg-muted text-foreground'
			)}
		>
			{flash.text}
		</div>
	{/if}
	<a href={back.href} class="tap inline-flex items-center text-xs text-muted-foreground hover:text-foreground">← {back.label}</a>

	<div class="mt-3 flex gap-4 sm:gap-5">
		{#if detail.request.poster_url?.startsWith('https://image.tmdb.org/')}
			<img src={detail.request.poster_url} alt="" class="h-28 w-[4.75rem] shrink-0 rounded-md bg-muted object-cover sm:h-36 sm:w-24" />
		{/if}
		<div class="min-w-0 flex-1">
			<h1 class="break-words text-xl font-semibold tracking-tight">
				{detail.request.title}
				{#if detail.request.year}<span class="font-normal text-muted-foreground">({detail.request.year})</span>{/if}
			</h1>
			<div class="mt-1 text-xs text-muted-foreground">
				{detail.request.media_type === 'tv' ? 'Series' : 'Movie'}
				{#if detail.request.requested_by}
					· requested by {detail.request.requested_by}{/if}
				{#if detail.request.seerr_request_id}
					· Seerr #{detail.request.seerr_request_id}{:else}
					· manual/orphan{/if}
				· updated {relativeTime(detail.request.updated_at, clock.now)}
			</div>

			<!-- Routine actions on the left; the destructive one set apart on the right
			     (on a phone it wraps onto its own line). -->
			<div class="mt-3 flex flex-wrap items-center gap-2">
				{#if resendAction}
					<button
						onclick={doResend}
						disabled={busy}
						title="Items completed but were never announced — send the notice now"
						class={cn(btn, 'border border-border hover:bg-accent')}
					>
						Resend notification
					</button>
				{/if}
				{#if detail.request.status === 'active' || detail.request.status === 'partial'}
					<button
						onclick={doCancel}
						disabled={busy}
						class={cn(btn, 'border border-destructive/40 text-destructive hover:bg-destructive/10')}
					>
						Cancel request
					</button>
				{/if}
				<button
					onclick={() => doRemove()}
					disabled={busy}
					title="Delete the series/movie in Sonarr/Radarr with its files, in Seerr, Jellyfin, the download client and here"
					class={cn(btn, 'ml-auto bg-destructive text-white hover:opacity-90')}
				>
					Remove everywhere
				</button>
			</div>

			<div class="mt-4 max-w-xl">
				<StageStrip counts={detail.request.stage_counts ?? {}} total={detail.request.item_count} />
			</div>
			{#if seasonActions.length > 0}
				<div class="mt-3 flex flex-wrap items-center gap-1.5">
					<span class="text-[11px] text-muted-foreground">Targeted search:</span>
					{#each seasonActions as a (a.id)}
						<button
							onclick={() => runSeason(a)}
							disabled={busy}
							class="tap rounded-md border border-border px-2 py-1 text-[11px] font-medium hover:bg-accent disabled:opacity-50"
						>
							Season {a.season}
						</button>
					{/each}
				</div>
			{/if}
		</div>
	</div>

	<div class="mt-8 grid grid-cols-1 gap-6 xl:grid-cols-5">
		<div class="min-w-0 xl:col-span-3">
			<h2 class="mb-2 text-sm font-medium text-muted-foreground">
				{detail.request.media_type === 'tv' ? `Episodes (${detail.items.length})` : 'Item'}
			</h2>
			<ul class="divide-y divide-border/60 overflow-hidden rounded-lg border border-border">
				{#each detail.items as item (item.id)}
					{@const open = selected?.id === item.id}
					<li>
						<button
							type="button"
							onclick={() => select(item)}
							aria-expanded={open}
							class={cn(
								'tap flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-accent/40',
								open && 'bg-accent/60'
							)}
						>
							<span class="w-16 shrink-0 font-mono text-xs text-muted-foreground">{epLabel(item)}</span>
							<span class="min-w-0 flex-1 truncate text-xs">{item.title}</span>
							<StageBadge stage={item.current_stage} error={!!item.last_error} />
							<span class="w-6 shrink-0 text-right text-[11px] text-muted-foreground">
								{item.current_cycle > 1 ? `×${item.current_cycle}` : ''}
							</span>
						</button>
						{#if open && !wide.current}
							<div class="border-t border-border/60 bg-card p-4">{@render timeline(item)}</div>
						{/if}
					</li>
				{/each}
			</ul>

			{#if detail.downloads.length > 0}
				<h2 class="mb-2 mt-6 text-sm font-medium text-muted-foreground">Downloads</h2>
				<div class="space-y-2">
					{#each detail.downloads as dl (dl.id)}
						{@const prog = live.progress[dl.id]}
						{@const bytes = prog?.bytes_downloaded ?? dl.bytes_downloaded ?? 0}
						{@const total = prog?.bytes_total ?? dl.bytes_total ?? dl.size_bytes ?? 0}
						<div class="rounded-lg border border-border bg-card px-3.5 py-2.5">
							<div class="flex items-center justify-between gap-3">
								<span class="truncate font-mono text-[11px]">{dl.release_title || dl.client_download_id}</span>
								<span class={cn('shrink-0 text-[11px] font-medium', dlStateColor[dl.state] ?? 'text-info')}>
									{dl.state}
								</span>
							</div>
							<div class="mt-1 flex items-center gap-3 text-[11px] text-muted-foreground">
								<span>{dl.arr}</span>
								{#if dl.source}<span>· {dl.source}</span>{/if}
								{#if dl.indexer}<span class="truncate">· {dl.indexer}</span>{/if}
								<span class="ml-auto shrink-0 tabular-nums">{formatBytesOrDash(bytes)} / {formatBytesOrDash(total)}</span>
							</div>
							{#if total > 0 && dl.state !== 'imported' && dl.state !== 'failed'}
								<div class="mt-1.5 h-1 overflow-hidden rounded-full bg-muted">
									<div class="h-full rounded-full bg-info transition-all" style="width: {Math.min(100, (bytes / total) * 100)}%"></div>
								</div>
							{/if}
							{#if dl.last_error}
								<p class="mt-1.5 break-all text-[11px] text-destructive">{dl.last_error}</p>
							{/if}
						</div>
					{/each}
				</div>
			{/if}
		</div>

		{#if wide.current}
			<div class="xl:col-span-2">
				{#if selected}
					<h2 class="mb-2 text-sm font-medium text-muted-foreground">
						Timeline — {epLabel(selected)}
						{#if selected.title}<span class="font-normal">· {selected.title}</span>{/if}
					</h2>
					<div class="rounded-lg border border-border bg-card p-4">{@render timeline(selected)}</div>
				{:else}
					<div class="rounded-lg border border-dashed border-border py-12 text-center text-xs text-muted-foreground">
						Select an item to see its timeline
					</div>
				{/if}
			</div>
		{/if}
	</div>
{/if}
