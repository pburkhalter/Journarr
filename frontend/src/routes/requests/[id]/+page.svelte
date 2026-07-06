<script lang="ts">
	import { page } from '$app/state';
	import { cancelRequest, getMediaEvents, getRequestDetail, retryItem } from '$lib/api';
	import StageBadge from '$lib/components/StageBadge.svelte';
	import StageStrip from '$lib/components/StageStrip.svelte';
	import StageTimeline from '$lib/components/StageTimeline.svelte';
	import { confirm } from '$lib/confirm.svelte';
	import { live } from '$lib/live.svelte';
	import type { ItemDetail, RawEvent, RequestDetail } from '$lib/types';
	import { cn, relativeTime } from '$lib/utils';

	let busy = $state(false);

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
		try {
			await cancelRequest(id);
			await loadDetail(id);
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
		try {
			await retryItem(item.id);
			await loadDetail(id);
		} finally {
			busy = false;
		}
	}

	const id = $derived(Number(page.params.id));

	let detail = $state<RequestDetail | null>(null);
	let selected = $state<ItemDetail | null>(null);
	let rawEvents = $state<RawEvent[] | null>(null);
	let rawError = $state(false);
	let notFound = $state(false);

	$effect(() => {
		void live.pipelineTick;
		void loadDetail(id);
	});

	// Reset page state when navigating between requests.
	$effect(() => {
		void id;
		notFound = false;
		detail = null;
		selected = null;
		rawEvents = null;
	});

	async function loadDetail(reqID: number) {
		if (!Number.isFinite(reqID)) return;
		try {
			const result = await getRequestDetail(reqID);
			if (reqID !== id) return; // stale response after navigation
			detail = result;
			notFound = false;
			if (selected) {
				selected = detail.items.find((i) => i.id === selected!.id) ?? null;
			}
			if (selected === null && detail.items.length === 1) {
				selected = detail.items[0];
			}
		} catch (e) {
			// api.ts throws `${path}: ${status}` — match the status suffix only
			if (reqID === id && /: 404$/.test((e as Error).message)) notFound = true;
		}
	}

	async function select(item: ItemDetail) {
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

	function fmtBytes(n?: number): string {
		if (!n || n <= 0) return '–';
		const units = ['B', 'KB', 'MB', 'GB', 'TB'];
		let i = 0;
		let v = n;
		while (v >= 1024 && i < units.length - 1) {
			v /= 1024;
			i++;
		}
		return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`;
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
</script>

{#if notFound}
	<p class="text-sm text-muted-foreground">Request not found. <a href="/" class="underline">Back to Flow</a></p>
{:else if detail}
	<a href="/" class="text-xs text-muted-foreground hover:text-foreground">← Flow</a>

	<div class="mt-3 flex gap-5">
		{#if detail.request.poster_url?.startsWith('https://image.tmdb.org/')}
			<img src={detail.request.poster_url} alt="" class="h-36 w-24 rounded-md object-cover bg-muted" />
		{/if}
		<div class="min-w-0 flex-1">
			<div class="flex items-start justify-between gap-3">
				<h1 class="text-xl font-semibold tracking-tight">
					{detail.request.title}
					{#if detail.request.year}<span class="font-normal text-muted-foreground">({detail.request.year})</span>{/if}
				</h1>
				{#if detail.request.status === 'active' || detail.request.status === 'partial'}
					<button
						onclick={doCancel}
						disabled={busy}
						class="shrink-0 rounded-md border border-destructive/40 px-2.5 py-1 text-xs font-medium text-destructive hover:bg-destructive/10 disabled:opacity-50"
					>
						Cancel request
					</button>
				{/if}
			</div>
			<div class="mt-1 text-xs text-muted-foreground">
				{detail.request.media_type === 'tv' ? 'Series' : 'Movie'}
				{#if detail.request.requested_by}
					· requested by {detail.request.requested_by}{/if}
				{#if detail.request.seerr_request_id}
					· Seerr #{detail.request.seerr_request_id}{:else}
					· manual/orphan{/if}
				· updated {relativeTime(detail.request.updated_at)}
			</div>
			<div class="mt-4 max-w-xl">
				<StageStrip counts={detail.request.stage_counts ?? {}} total={detail.request.item_count} />
			</div>
		</div>
	</div>

	<div class="mt-8 grid grid-cols-1 gap-6 xl:grid-cols-5">
		<div class="xl:col-span-3">
			<h2 class="mb-2 text-sm font-medium text-muted-foreground">
				{detail.request.media_type === 'tv' ? `Episodes (${detail.items.length})` : 'Item'}
			</h2>
			<div class="overflow-hidden rounded-lg border border-border">
				<table class="w-full text-sm">
					<tbody>
						{#each detail.items as item (item.id)}
							<tr
								class={cn(
									'cursor-pointer border-b border-border/60 last:border-0 hover:bg-accent/40',
									selected?.id === item.id && 'bg-accent/60'
								)}
								onclick={() => select(item)}
							>
								<td class="w-20 px-3 py-2 font-mono text-xs text-muted-foreground">{epLabel(item)}</td>
								<td class="max-w-0 truncate px-2 py-2 text-xs">{item.title}</td>
								<td class="w-28 px-2 py-2 text-right">
									<StageBadge stage={item.current_stage} error={!!item.last_error} />
								</td>
								<td class="w-10 px-2 py-2 text-right text-[11px] text-muted-foreground">
									{item.current_cycle > 1 ? `×${item.current_cycle}` : ''}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>

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
								<span class="ml-auto tabular-nums">{fmtBytes(bytes)} / {fmtBytes(total)}</span>
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

		<div class="xl:col-span-2">
			{#if selected}
				<h2 class="mb-2 text-sm font-medium text-muted-foreground">
					Timeline — {epLabel(selected)}
					{#if selected.title}<span class="font-normal">· {selected.title}</span>{/if}
				</h2>
				<div class="rounded-lg border border-border bg-card p-4">
					{#if selected.current_stage !== 'available' && selected.current_stage !== 'notified'}
						<div class="mb-3 flex justify-end">
							<button
								onclick={() => selected && doRetry(selected)}
								disabled={busy}
								class="rounded-md border border-border px-2.5 py-1 text-xs font-medium hover:bg-accent disabled:opacity-50"
							>
								Retry
							</button>
						</div>
					{/if}
					{#if selected.stuck_since}
						<p class="mb-3 rounded-md bg-warning/10 px-2.5 py-1.5 text-[11px] text-warning">
							⏳ Stuck — no progress since {relativeTime(selected.stuck_since)}
						</p>
					{/if}
					{#if selected.last_error}
						<p class="mb-3 break-all rounded-md bg-destructive/10 px-2.5 py-1.5 text-[11px] text-destructive">
							{selected.last_error}
						</p>
					{/if}
					<StageTimeline transitions={selected.transitions} currentCycle={selected.current_cycle} />
					{#if selected.imported_path}
						<p class="mt-2 break-all border-t border-border pt-2 text-[11px] text-muted-foreground">
							{selected.imported_path}
						</p>
					{/if}
					<button
						onclick={loadRaw}
						class="mt-3 text-[11px] text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
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
										· {ev.received_at}
									</summary>
									<pre class="mt-1 overflow-x-auto text-[10px] leading-relaxed">{JSON.stringify(ev.payload, null, 2)}</pre>
								</details>
							{/each}
						</div>
					{/if}
				</div>
			{:else}
				<div class="rounded-lg border border-dashed border-border py-12 text-center text-xs text-muted-foreground">
					Select an item to see its timeline
				</div>
			{/if}
		</div>
	</div>
{/if}
