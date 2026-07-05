<script lang="ts">
	import ServiceCard from '$lib/components/ServiceCard.svelte';
	import { live } from '$lib/live.svelte';

	// Stable display order: pipeline order, not alphabetical.
	const order = ['seerr', 'sonarr', 'radarr', 'prowlarr', 'arrarr', 'jellyfin', 'waha'];

	const sorted = $derived(
		Object.values(live.services).sort((a, b) => {
			const ia = order.indexOf(a.service);
			const ib = order.indexOf(b.service);
			return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib);
		})
	);
</script>

<div class="mb-6">
	<h1 class="text-xl font-semibold tracking-tight">Services</h1>
	<p class="text-sm text-muted-foreground">Live health of every tool in the pipeline.</p>
</div>

{#if sorted.length === 0}
	<div class="max-w-xl rounded-lg border border-dashed border-border py-16 text-center">
		<div class="text-sm font-medium">No services configured</div>
		<p class="mt-1 text-xs text-muted-foreground">
			Set SEERR_URL, SONARR_URL, … in the backend environment.
		</p>
	</div>
{:else}
	<div class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
		{#each sorted as svc (svc.service)}
			<ServiceCard service={svc} />
		{/each}
	</div>
{/if}
