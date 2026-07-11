<script lang="ts">
	import { getSessions, jellyfinScan } from '$lib/api';
	import ServiceCard from '$lib/components/ServiceCard.svelte';
	import { confirm } from '$lib/confirm.svelte';
	import { live } from '$lib/live.svelte';
	import type { JellySession } from '$lib/types';

	// Live Jellyfin now-playing, folded into the Jellyfin tile (no separate card).
	let streams = $state<JellySession[]>([]);
	$effect(() => {
		const load = async () => {
			try {
				streams = (await getSessions()).sessions;
			} catch {
				// jellyfin unreachable — the tile status shows it
			}
		};
		void load();
		const t = setInterval(load, 8000);
		return () => clearInterval(t);
	});

	let scanning = $state(false);
	let scanMsg = $state('');
	async function doScan() {
		if (!(await confirm.ask({ title: 'Trigger Jellyfin scan', message: 'Start a full library refresh in Jellyfin?', confirmLabel: 'Scan' }))) return;
		scanning = true;
		scanMsg = '';
		try {
			await jellyfinScan();
			scanMsg = 'scan triggered';
		} catch {
			scanMsg = 'failed';
		} finally {
			scanning = false;
		}
	}

	// Tiles derive from the registry (order, label, folding). Instances with a
	// parent_id (e.g. waha → concierge) are folded and not shown standalone.
	// Falls back to the historical hardcoded order if /api/instances is absent.
	const fallbackOrder = ['seerr', 'sonarr', 'radarr', 'prowlarr', 'arrarr', 'jellyfin', 'waha', 'concierge'];

	const sorted = $derived.by(() => {
		if (live.instances.length > 0) {
			return live.instances
				.filter((i) => !i.parent_id)
				.map((i) => ({ health: live.services[i.id], label: i.label }))
				.filter((x): x is { health: (typeof live.services)[string]; label: string } => !!x.health);
		}
		return Object.values(live.services)
			.sort((a, b) => {
				const ia = fallbackOrder.indexOf(a.service);
				const ib = fallbackOrder.indexOf(b.service);
				return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib);
			})
			.map((health) => ({ health, label: undefined as string | undefined }));
	});
</script>

<div class="mb-6 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
	<div>
		<h1 class="text-xl font-semibold tracking-tight">Services</h1>
		<p class="text-sm text-muted-foreground">Live health of every tool in the pipeline.</p>
	</div>
	<div class="flex items-center gap-2">
		{#if scanMsg}<span class="text-[11px] text-muted-foreground">{scanMsg}</span>{/if}
		<button
			onclick={doScan}
			disabled={scanning}
			class="shrink-0 rounded-md border border-border px-3 py-1.5 text-xs font-medium hover:bg-accent disabled:opacity-50"
		>
			{scanning ? 'Scanning…' : 'Jellyfin scan'}
		</button>
	</div>
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
		{#each sorted as x (x.health.service)}
			<ServiceCard
				service={x.health}
				label={x.label}
				streams={x.health.service === 'jellyfin' ? streams : undefined}
			/>
		{/each}
	</div>
{/if}
