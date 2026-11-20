<script lang="ts">
	import { executeAction, getActions, getFlowSettings, putFlowSettings } from '#lib/api.ts';
	import { confirm } from '#lib/confirm.svelte.ts';
	import { confirmJellyfinScan } from '#lib/prompts.ts';
	import type { Action } from '#lib/types.ts';
	import { cn } from '#lib/utils.ts';

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

	const searchActions = $derived(actions.filter((a) => a.kind === 'search-missing'));
	const libraryActions = $derived(actions.filter((a) => a.kind === 'library-scan'));
	const transcodeActions = $derived(actions.filter((a) => a.kind.startsWith('transcode-')));

	function paramsFor(a: Action): Record<string, unknown> {
		if (a.kind === 'search-missing') return { instance_id: a.instance_id };
		return {};
	}

	// Same question as anywhere else the same action is offered.
	function confirmRun(a: Action): Promise<boolean> {
		if (a.kind === 'search-missing') {
			return confirm.ask({
				title: a.label,
				message: 'Trigger a search for all missing monitored items? This can start many downloads.',
				confirmLabel: 'Search'
			});
		}
		if (a.kind === 'library-scan') return confirmJellyfinScan();
		return Promise.resolve(true);
	}

	async function run(a: Action) {
		if (!(await confirmRun(a))) return;
		busy[a.id] = true;
		result[a.id] = '';
		try {
			await executeAction(a.kind, paramsFor(a));
			result[a.id] = 'triggered';
		} catch {
			result[a.id] = 'failed';
		} finally {
			busy[a.id] = false;
		}
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

{#if searchActions.length > 0}
	<section class="mb-6">
		<h2 class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Search</h2>
		<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
			{#each searchActions as a (a.id)}
				{@render actionCard(a, 'Search for all missing, monitored items')}
			{/each}
		</div>
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
