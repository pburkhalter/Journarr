<script lang="ts">
	import { executeAction, getActions, getFlowSettings, putFlowSettings, recheckOpen } from '#lib/api.ts';
	import { clock } from '#lib/clock.svelte.ts';
	import { confirm } from '#lib/confirm.svelte.ts';
	import { confirmJellyfinScan } from '#lib/prompts.ts';
	import type { Action, RecheckOutcome, RecheckReport, RecheckRequest } from '#lib/types.ts';
	import { cn, relativeTime } from '#lib/utils.ts';

	let actions = $state<Action[]>([]);
	let loaded = $state(false);
	let busy = $state<Record<string, boolean>>({});
	let result = $state<Record<string, string>>({});

	// Control-plane (Flow) settings.
	let flow = $state<Record<string, string>>({});
	let flowSaving = $state(false);
	let flowError = $state('');
	const flowOn = (k: string) => flow[k] === 'true' || flow[k] === '1' || flow[k] === 'on';
	const retryMin = $derived(Math.round((Number(flow['auto_retry_stuck_after_secs']) || 0) / 60));

	async function saveFlow(patch: Record<string, string>) {
		flow = { ...flow, ...patch };
		flowSaving = true;
		flowError = '';
		try {
			flow = await putFlowSettings(patch);
		} catch {
			// say so, then reload the authoritative state
			flowError = 'Could not save the change — the setting shows what is stored.';
			try {
				flow = await getFlowSettings();
			} catch {
				/* keep optimistic */
			}
		} finally {
			flowSaving = false;
		}
	}

	async function load() {
		try {
			actions = await getActions('global');
		} catch {
			actions = [];
		}
		try {
			flow = await getFlowSettings();
		} catch {
			flow = {};
		}
		loaded = true;
	}
	$effect(() => {
		void load();
	});

	const recheckActions = $derived(actions.filter((a) => a.kind === 'recheck-open'));
	const libraryActions = $derived(actions.filter((a) => a.kind === 'library-scan'));
	const transcodeActions = $derived(actions.filter((a) => a.kind.startsWith('transcode-')));

	// Same question as anywhere else the same action is offered.
	function confirmRun(a: Action): Promise<boolean> {
		if (a.kind === 'recheck-open') {
			return confirm.ask({
				title: a.label,
				message:
					'Checks every open request against Sonarr/Radarr and searches once for everything released or aired that is still missing. It does not search again within 15 minutes. Items shown as available without a file in Sonarr/Radarr go back to the stage they really reached, also in requests shown as completed.',
				confirmLabel: 'Re-check'
			});
		}
		if (a.kind === 'library-scan') return confirmJellyfinScan();
		return Promise.resolve(true);
	}

	// The last re-check's findings, shown under its card.
	let report = $state<RecheckReport | null>(null);

	async function run(a: Action) {
		if (!(await confirmRun(a))) return;
		busy[a.id] = true;
		result[a.id] = '';
		try {
			if (a.kind === 'recheck-open') {
				report = await recheckOpen();
				result[a.id] = 'done';
			} else {
				await executeAction(a.kind, {});
				result[a.id] = 'triggered';
			}
		} catch {
			result[a.id] = 'failed';
		} finally {
			busy[a.id] = false;
		}
	}

	// Request outcomes, most telling first, with what each means.
	const outcomeInfo: { key: RecheckOutcome; label: string; hint?: string; tone: string }[] = [
		{ key: 'error', label: 'Could not check', tone: 'text-destructive' },
		{
			key: 'no_file',
			label: 'Shown as available without a file — taken back',
			hint: 'Jellyfin or Seerr said so, but Sonarr/Radarr have no file. The items are back at the stage they really reached.',
			tone: 'text-warning'
		},
		{
			key: 'not_in_arr',
			label: 'Not in Sonarr/Radarr',
			hint: 'Seerr could not add the title — check the request in Seerr.',
			tone: 'text-warning'
		},
		{ key: 'reconciled', label: 'Already on disk — now marked imported', tone: 'text-success' },
		{ key: 'reset', label: 'Failed attempt — started again', tone: 'text-info' },
		{
			key: 'missing',
			label: 'Out but missing — searched',
			hint: 'If the search finds nothing, no release matches the quality profile yet (often: no German release).',
			tone: 'text-foreground'
		},
		{ key: 'unmonitored', label: 'Not monitored in Sonarr/Radarr — not searched', tone: 'text-muted-foreground' },
		{ key: 'downloading', label: 'Downloading', tone: 'text-muted-foreground' },
		{ key: 'waiting', label: 'Not out yet', tone: 'text-muted-foreground' },
		{ key: 'done', label: 'Imported — waiting for Jellyfin', tone: 'text-muted-foreground' }
	];
	const countWord: Record<RecheckOutcome, string> = {
		error: 'error',
		no_file: 'no file',
		not_in_arr: 'unknown',
		reconciled: 'on disk',
		reset: 'reset',
		missing: 'missing',
		unmonitored: 'unmonitored',
		downloading: 'downloading',
		waiting: 'not out yet',
		done: 'done'
	};
	const groups = $derived(
		report
			? outcomeInfo
					.map((o) => ({ ...o, requests: report!.requests.filter((r) => r.outcome === o.key) }))
					.filter((g) => g.requests.length > 0)
			: []
	);

	// "15 missing · 2 not out yet" — only for series, a film is one item.
	function itemCounts(r: RecheckRequest): string {
		if (r.media_type !== 'tv') return '';
		return outcomeInfo
			.filter((o) => (r.counts[o.key] ?? 0) > 0)
			.map((o) => `${r.counts[o.key]} ${countWord[o.key]}`)
			.join(' · ');
	}
</script>

<div class="mb-6">
	<h1 class="text-xl font-semibold tracking-tight">Actions</h1>
	<p class="text-sm text-muted-foreground">
		Interventions available from your configured tools — derived automatically from what each
		one supports.
	</p>
</div>

{#if loaded && actions.length === 0}
	<div class="max-w-xl rounded-lg border border-dashed border-border py-16 text-center">
		<div class="text-sm font-medium">No actions available</div>
		<p class="mt-1 text-xs text-muted-foreground">
			Configure a Sonarr/Radarr (search) or Jellyfin (library scan) instance.
		</p>
	</div>
{/if}

{#snippet actionCard(a: Action, hint: string)}
	<div class="flex items-center justify-between gap-3 rounded-lg border border-border bg-card p-4">
		<div class="min-w-0">
			<div class="truncate text-sm font-medium">{a.label}</div>
			<div class="text-[11px] text-muted-foreground">{hint}</div>
		</div>
		<div class="flex shrink-0 items-center gap-2">
			{#if result[a.id]}
				<span class={cn('text-[11px]', result[a.id] === 'failed' ? 'text-destructive' : 'text-muted-foreground')}>
					{result[a.id]}
				</span>
			{/if}
			<button
				onclick={() => run(a)}
				disabled={busy[a.id]}
				class={cn(
					'tap shrink-0 rounded-md border px-3 py-1.5 text-xs font-medium disabled:opacity-50',
					a.danger
						? 'border-destructive/40 text-destructive hover:bg-destructive/10'
						: 'border-border hover:bg-accent'
				)}
			>
				{busy[a.id] ? 'Running…' : 'Run'}
			</button>
		</div>
	</div>
{/snippet}

{#if recheckActions.length > 0}
	<section class="mb-6">
		<h2 class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Open requests</h2>
		<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
			{#each recheckActions as a (a.id)}
				{@render actionCard(a, 'Match with Sonarr/Radarr, take back what has no file, restart failed attempts, search what is out')}
			{/each}
		</div>
		{#if report}
			<div class="mt-3 rounded-lg border border-border bg-card p-4" aria-live="polite">
				<p class="text-sm">
					<span class="font-medium">{report.open} open {report.open === 1 ? 'request' : 'requests'} checked.</span>
					{#if report.search_skipped && report.searched_at}
						<span class="text-muted-foreground">
							Searched {relativeTime(report.searched_at, clock.now)} already — not again within 15 minutes.
						</span>
					{:else if report.searched_at}
						<span class="text-muted-foreground">Searches started.</span>
					{:else}
						<span class="text-muted-foreground">Nothing to search.</span>
					{/if}
				</p>
				{#if report.errors?.length}
					<ul class="mt-2 space-y-0.5 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive" role="alert">
						{#each report.errors as e (e)}<li class="break-words">{e}</li>{/each}
					</ul>
				{/if}
				{#each groups as g (g.key)}
					<div class="mt-4">
						<h3 class={cn('text-xs font-semibold', g.tone)}>
							{g.key === 'missing' && report.search_skipped ? 'Out but missing — searched shortly before' : g.label}
							<span class="font-normal text-muted-foreground">({g.requests.length})</span>
						</h3>
						{#if g.hint}<p class="mt-0.5 text-[11px] text-muted-foreground">{g.hint}</p>{/if}
						<ul class="mt-1.5 divide-y divide-border/60">
							{#each g.requests as r (r.id)}
								<li>
									<a
										href="/requests/{r.id}"
										class="tap flex items-baseline justify-between gap-3 py-1.5 text-sm hover:underline"
									>
										<span class="min-w-0 truncate">{r.title || `Request ${r.id}`}</span>
										{#if itemCounts(r)}
											<span class="shrink-0 text-[11px] text-muted-foreground tabular-nums">{itemCounts(r)}</span>
										{/if}
									</a>
								</li>
							{/each}
						</ul>
					</div>
				{/each}
			</div>
		{/if}
	</section>
{/if}

{#if libraryActions.length > 0}
	<section class="mb-6">
		<h2 class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Library</h2>
		<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
			{#each libraryActions as a (a.id)}
				{@render actionCard(a, 'Trigger a full library refresh')}
			{/each}
		</div>
	</section>
{/if}

{#if transcodeActions.length > 0}
	<section class="mb-6">
		<h2 class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Transcoding</h2>
		<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
			{#each transcodeActions as a (a.id)}
				{@render actionCard(
					a,
					a.kind === 'transcode-scan'
						? 'Rescan libraries for new/changed files'
						: a.kind === 'transcode-pause'
							? 'Pause the transcode workers'
							: 'Resume the transcode workers'
				)}
			{/each}
		</div>
	</section>
{/if}

{#if loaded}
	<section class="mb-6">
		<h2 class="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
			Automation {#if flowSaving}<span class="text-[10px] normal-case text-muted-foreground/60">saving…</span>{/if}
		</h2>
		<p class="mb-3 max-w-2xl text-xs text-muted-foreground">
			Interventions Journarr performs on its own. Off by default — the download pipeline runs
			the same either way.
		</p>
		{#if flowError}
			<p role="alert" class="mb-3 max-w-2xl rounded-md bg-destructive/10 px-3 py-1.5 text-xs text-destructive">{flowError}</p>
		{/if}
		<div class="space-y-3">
			<div class="flex items-center justify-between gap-3 rounded-lg border border-border bg-card p-4">
				<span class="min-w-0">
					<span class="block text-sm font-medium">Auto-retry stuck items</span>
					<span class="text-[11px] text-muted-foreground">Re-search items with no progress for this long. 0 = off.</span>
				</span>
				<span class="flex shrink-0 items-center gap-1.5">
					<input
						type="number"
						min="0"
						value={retryMin}
						onchange={(e) =>
							saveFlow({ auto_retry_stuck_after_secs: String(Math.max(0, Number(e.currentTarget.value) || 0) * 60) })}
						aria-label="Minutes without progress before a retry"
						class="h-10 w-20 rounded-md border border-border bg-background px-2 py-1 text-base tabular-nums sm:h-auto sm:w-16 sm:text-xs"
					/>
					<span class="text-[11px] text-muted-foreground">min</span>
				</span>
			</div>
			<label class="flex items-center justify-between gap-3 rounded-lg border border-border bg-card p-4">
				<span class="min-w-0">
					<span class="block text-sm font-medium">Send notifications via Journarr</span>
					<span class="text-[11px] text-muted-foreground">
						Journarr triggers the WhatsApp notice on completion &amp; records the Notified stage.
						Requires notifyarr <code class="text-foreground">NOTIFY_MODE=journarr</code> + a shared token.
					</span>
				</span>
				<input
					type="checkbox"
					class="size-4 shrink-0 accent-primary"
					checked={flowOn('notify_on_complete')}
					onchange={(e) => saveFlow({ notify_on_complete: e.currentTarget.checked ? 'true' : 'false' })}
				/>
			</label>
		</div>
	</section>
{/if}
