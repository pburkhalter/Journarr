<script lang="ts">
	import { getRequests } from '$lib/api';
	import PipelineCard from '$lib/components/PipelineCard.svelte';
	import { live } from '$lib/live.svelte';
	import type { RequestRollup } from '$lib/types';
	import { cn } from '$lib/utils';

	let requests = $state<RequestRollup[]>([]);
	let filter = $state<'active' | 'waiting' | 'done' | 'all'>('active');
	let query = $state('');
	let loaded = $state(false);

	// Sequence guard: a slow older response must never overwrite a newer one.
	let seq = 0;

	// The server owns the waiting split: "waiting" returns anything with a
	// future release date (unreleased movies + TV series awaiting their next
	// episode), and excludes those from active/done.
	async function load(f: string, q: string) {
		const mySeq = ++seq;
		try {
			const result = await getRequests(f, q.trim());
			if (mySeq === seq) {
				requests = result;
				loaded = true;
			}
		} catch {
			// backend unreachable — sidebar shows it
		}
	}

	const visible = $derived(requests);

	// initial + live refresh (debounced tick from SSE) + filter changes.
	// `query` is intentionally NOT read here — the search box has its own
	// debounce; reading it in the effect would refire on every keystroke.
	$effect(() => {
		void live.pipelineTick;
		const f = filter;
		void load(f, query);
	});

	let searchTimer: ReturnType<typeof setTimeout>;
	function onSearch() {
		clearTimeout(searchTimer);
		searchTimer = setTimeout(() => void load(filter, query), 300);
	}

	const filters = [
		{ key: 'active', label: 'Active' },
		{ key: 'waiting', label: 'Waiting' },
		{ key: 'done', label: 'Done' },
		{ key: 'all', label: 'All' }
	] as const;
</script>

<div class="mb-6 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between sm:gap-4">
	<div>
		<h1 class="text-xl font-semibold tracking-tight">Flow</h1>
		<p class="text-sm text-muted-foreground">Every request, end to end.</p>
	</div>
	<div class="flex flex-wrap items-center gap-2">
		<input
			type="search"
			placeholder="Search…"
			bind:value={query}
			oninput={onSearch}
			class="h-8 min-w-0 flex-1 basis-40 rounded-md border border-input bg-transparent px-2.5 text-sm outline-none placeholder:text-muted-foreground focus:ring-1 focus:ring-ring sm:w-44 sm:flex-none sm:basis-auto"
		/>
		<div class="flex shrink-0 rounded-md border border-border p-0.5">
			{#each filters as f (f.key)}
				<button
					onclick={() => (filter = f.key)}
					class={cn(
						'rounded px-2.5 py-1 text-xs transition-colors',
						filter === f.key ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:text-foreground'
					)}
				>
					{f.label}
				</button>
			{/each}
		</div>
	</div>
</div>

{#if visible.length === 0 && loaded}
	<div class="flex max-w-2xl flex-col items-center rounded-lg border border-dashed border-border py-16 text-center">
		<div class="text-sm font-medium">
			{#if filter === 'active'}Nothing in flight{:else if filter === 'waiting'}Nothing waiting for release{:else}No requests found{/if}
		</div>
		<p class="mt-1 max-w-sm text-xs text-muted-foreground">
			{#if filter === 'waiting'}
				Movies not yet released and series awaiting their next episode show up here until their air date.
			{:else}
				New Seerr requests and arr grabs appear here automatically, live.
			{/if}
		</p>
	</div>
{:else}
	<div class="grid grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3">
		{#each visible as r (r.id)}
			<PipelineCard request={r} />
		{/each}
	</div>
{/if}
