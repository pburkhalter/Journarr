<script lang="ts">
	import { getRequests } from '$lib/api';
	import PipelineCard from '$lib/components/PipelineCard.svelte';
	import { live } from '$lib/live.svelte';
	import type { RequestRollup } from '$lib/types';

	let requests = $state<RequestRollup[]>([]);
	let query = $state('');
	let loaded = $state(false);
	let seq = 0;

	async function load(q: string) {
		const mySeq = ++seq;
		try {
			const result = await getRequests('done', q.trim());
			if (mySeq === seq) {
				requests = result;
				loaded = true;
			}
		} catch {
			// backend down — sidebar shows it
		}
	}

	$effect(() => {
		void live.pipelineTick;
		void load(query);
	});

	let searchTimer: ReturnType<typeof setTimeout>;
	function onSearch() {
		clearTimeout(searchTimer);
		searchTimer = setTimeout(() => void load(query), 300);
	}
</script>

<div class="mb-6 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
	<div>
		<h1 class="text-xl font-semibold tracking-tight">History</h1>
		<p class="text-sm text-muted-foreground">Completed, canceled and failed requests.</p>
	</div>
	<input
		type="search"
		placeholder="Search…"
		bind:value={query}
		oninput={onSearch}
		class="h-8 min-w-0 rounded-md border border-input bg-transparent px-2.5 text-sm outline-none placeholder:text-muted-foreground focus:ring-1 focus:ring-ring sm:w-56"
	/>
</div>

{#if requests.length === 0 && loaded}
	<div class="max-w-2xl rounded-lg border border-dashed border-border py-16 text-center">
		<div class="text-sm font-medium">Nothing here yet</div>
		<p class="mt-1 text-xs text-muted-foreground">Finished requests will collect here.</p>
	</div>
{:else}
	<div class="grid grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3">
		{#each requests as r (r.id)}
			<PipelineCard request={r} />
		{/each}
	</div>
{/if}
