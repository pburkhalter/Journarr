<script lang="ts">
	import { getRequests } from '$lib/api';
	import PipelineCard from '$lib/components/PipelineCard.svelte';
	import { live } from '$lib/live.svelte';
	import type { RequestRollup } from '$lib/types';
	import { cn } from '$lib/utils';

	let requests = $state<RequestRollup[]>([]);
	let filter = $state<'active' | 'done' | 'all'>('active');
	let query = $state('');
	let loaded = $state(false);

	// Sequence guard: a slow older response must never overwrite a newer one.
	let seq = 0;

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
		{ key: 'done', label: 'Done' },
		{ key: 'all', label: 'All' }
	] as const;
</script>

<div class="mb-6 flex items-end justify-between gap-4">
	<div>
		<h1 class="text-xl font-semibold tracking-tight">Flow</h1>
		<p class="text-sm text-muted-foreground">Every request, end to end.</p>
	</div>
	<div class="flex items-center gap-2">
		<input
			type="search"
			placeholder="Search…"
			bind:value={query}
			oninput={onSearch}
			class="h-8 w-44 rounded-md border border-input bg-transparent px-2.5 text-sm outline-none placeholder:text-muted-foreground focus:ring-1 focus:ring-ring"
		/>
		<div class="flex rounded-md border border-border p-0.5">
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

{#if requests.length === 0 && loaded}
	<div class="flex max-w-2xl flex-col items-center rounded-lg border border-dashed border-border py-16 text-center">
		<div class="text-sm font-medium">
			{filter === 'active' ? 'Nothing in flight' : 'No requests found'}
		</div>
		<p class="mt-1 max-w-sm text-xs text-muted-foreground">
			New Seerr requests and arr grabs appear here automatically, live.
		</p>
	</div>
{:else}
	<div class="grid grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3">
		{#each requests as r (r.id)}
			<PipelineCard request={r} />
		{/each}
	</div>
{/if}
