<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { getRequests } from '$lib/api';
	import PipelineCard from '$lib/components/PipelineCard.svelte';
	import { live } from '$lib/live.svelte';
	import type { RequestRollup } from '$lib/types';
	import { cn } from '$lib/utils';
	import { untrack } from 'svelte';

	// Filter and search live in the URL (?status=done&q=dune), so going back
	// from a request, reloading or sharing the link keeps the view. History is
	// this page with status=done (/history redirects here).
	const filters = [
		{ key: 'active', label: 'Active' },
		{ key: 'waiting', label: 'Waiting' },
		{ key: 'done', label: 'Done' },
		{ key: 'all', label: 'All' }
	] as const;
	type Filter = (typeof filters)[number]['key'];

	const filter = $derived.by((): Filter => {
		const s = page.url.searchParams.get('status');
		return filters.find((f) => f.key === s)?.key ?? 'active';
	});
	const urlQuery = $derived(page.url.searchParams.get('q') ?? '');
	const isHistory = $derived(filter === 'done');

	let requests = $state<RequestRollup[]>([]);
	let loaded = $state(false);

	// Sequence guard: a slow older response must never overwrite a newer one.
	let seq = 0;

	// The server owns the waiting split: "waiting" returns anything with a
	// future release date (unreleased movies + TV series awaiting their next
	// episode), and excludes those from active/done.
	async function load(f: string, q: string) {
		const mySeq = ++seq;
		try {
			const result = await getRequests(f, q);
			if (mySeq === seq) {
				requests = result;
				loaded = true;
			}
		} catch {
			// backend unreachable — sidebar shows it
		}
	}

	// Initial + live refresh (debounced tick from SSE) + URL changes. The
	// search box only reaches the URL after its debounce, so typing does not
	// fire a request per keystroke.
	$effect(() => {
		void live.pipelineTick;
		void load(filter, urlQuery);
	});

	// The text in the box. Follows the URL when it changes from outside
	// (back/forward, a nav click), but not while the user is typing.
	let query = $state(untrack(() => urlQuery));
	let typing = false;
	$effect(() => {
		const q = urlQuery;
		// untrack: the box itself must not re-run this; and leave "star " alone
		// when the URL already says "star".
		if (!typing && untrack(() => query.trim()) !== q) query = q;
	});

	function setUrl(status: Filter, q: string, replace: boolean) {
		const params = new URLSearchParams();
		if (status !== 'active') params.set('status', status);
		if (q) params.set('q', q);
		const qs = params.toString();
		void goto(qs ? `/?${qs}` : '/', { replaceState: replace, keepFocus: true, noScroll: true });
	}

	let searchTimer: ReturnType<typeof setTimeout>;
	function onSearch() {
		typing = true;
		clearTimeout(searchTimer);
		searchTimer = setTimeout(() => {
			typing = false;
			setUrl(filter, query.trim(), true);
		}, 300);
	}
</script>

<div class="mb-6 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between sm:gap-4">
	<div>
		<h1 class="text-xl font-semibold tracking-tight">{isHistory ? 'History' : 'Flow'}</h1>
		<p class="text-sm text-muted-foreground">
			{isHistory ? 'Completed, canceled and failed requests.' : 'Every request, end to end.'}
		</p>
	</div>
	<div class="flex flex-wrap items-center gap-2">
		<input
			type="search"
			placeholder="Search…"
			aria-label="Search requests"
			bind:value={query}
			oninput={onSearch}
			class="h-10 min-w-0 flex-1 basis-40 rounded-md border border-input bg-transparent px-2.5 text-base outline-none placeholder:text-muted-foreground focus:ring-1 focus:ring-ring sm:h-8 sm:w-44 sm:flex-none sm:basis-auto sm:text-sm"
		/>
		<div class="flex shrink-0 rounded-md border border-border p-0.5" role="group" aria-label="Filter">
			{#each filters as f (f.key)}
				<button
					onclick={() => setUrl(f.key, urlQuery, false)}
					aria-pressed={filter === f.key}
					class={cn(
						'tap rounded px-2.5 py-1 text-xs transition-colors',
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
			{#if urlQuery}No requests match “{urlQuery}”{:else if filter === 'active'}Nothing in flight{:else if filter === 'waiting'}Nothing waiting for release{:else if filter === 'done'}Nothing here yet{:else}No requests found{/if}
		</div>
		<p class="mt-1 max-w-sm text-xs text-muted-foreground">
			{#if filter === 'waiting'}
				Movies not yet released and series awaiting their next episode show up here until their air date.
			{:else if filter === 'done'}
				Finished requests collect here.
			{:else}
				New Seerr requests and arr grabs appear here automatically, live.
			{/if}
		</p>
	</div>
{:else}
	<div class="grid grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3">
		{#each requests as r (r.id)}
			<PipelineCard request={r} />
		{/each}
	</div>
{/if}
