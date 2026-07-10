<script lang="ts">
	import { executeAction, getActions } from '$lib/api';
	import { confirm } from '$lib/confirm.svelte';
	import type { Action } from '$lib/types';
	import { cn } from '$lib/utils';

	let actions = $state<Action[]>([]);
	let loaded = $state(false);
	let busy = $state<Record<string, boolean>>({});
	let result = $state<Record<string, string>>({});

	async function load() {
		try {
			actions = await getActions('global');
		} catch {
			actions = [];
		}
		loaded = true;
	}
	$effect(() => {
		void load();
	});

	const searchActions = $derived(actions.filter((a) => a.kind === 'search-missing'));
	const libraryActions = $derived(actions.filter((a) => a.kind === 'library-scan'));

	function paramsFor(a: Action): Record<string, unknown> {
		if (a.kind === 'search-missing') return { instance_id: a.instance_id };
		return {};
	}

	async function run(a: Action) {
		if (
			a.kind === 'search-missing' &&
			!(await confirm.ask({
				title: a.label,
				message: 'Trigger a search for all missing monitored items? This can start many downloads.',
				confirmLabel: 'Search'
			}))
		)
			return;
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
					'shrink-0 rounded-md border px-3 py-1.5 text-xs font-medium disabled:opacity-50',
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
