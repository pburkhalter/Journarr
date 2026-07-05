<script lang="ts">
	import { getStats } from '$lib/api';
	import type { Stats } from '$lib/types';
	import { onMount } from 'svelte';

	let stats = $state<Stats | null>(null);

	onMount(async () => {
		try {
			stats = await getStats();
		} catch {
			// backend down — sidebar indicator already shows it
		}
	});

	const active = $derived(stats?.requests['active'] ?? 0);
	const completed = $derived(stats?.requests['completed'] ?? 0);
	const failed = $derived(stats?.requests['failed'] ?? 0);
</script>

<div class="mb-6">
	<h1 class="text-xl font-semibold tracking-tight">Flow</h1>
	<p class="text-sm text-muted-foreground">Every request, end to end.</p>
</div>

<div class="mb-8 grid grid-cols-3 gap-4 max-w-xl">
	{#each [{ label: 'Active', value: active }, { label: 'Completed', value: completed }, { label: 'Failed', value: failed }] as tile (tile.label)}
		<div class="rounded-lg border border-border bg-card p-4">
			<div class="text-2xl font-semibold tabular-nums">{tile.value}</div>
			<div class="text-xs text-muted-foreground">{tile.label}</div>
		</div>
	{/each}
</div>

<div class="flex max-w-xl flex-col items-center rounded-lg border border-dashed border-border py-16 text-center">
	<div class="text-sm font-medium">No tracked requests yet</div>
	<p class="mt-1 max-w-sm text-xs text-muted-foreground">
		Pipeline ingestion (Seerr / Sonarr / Radarr webhooks) lands with milestone 1. Until then, the
		Services page shows the health of the whole stack.
	</p>
	<a
		href="/services"
		class="mt-4 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:opacity-90"
	>
		View services
	</a>
</div>
