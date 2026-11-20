<script lang="ts">
	import { getDiskSpace, getSessions, jellyfinScan } from '#lib/api.ts';
	import { clock } from '#lib/clock.svelte.ts';
	import DiskSpaceCard from '#lib/components/DiskSpaceCard.svelte';
	import ServiceCard from '#lib/components/ServiceCard.svelte';
	import { live } from '#lib/live.svelte.ts';
	import { confirmJellyfinScan } from '#lib/prompts.ts';
	import type { DiskMount, JellySession, ServiceHealth } from '#lib/types.ts';
	import { cn } from '#lib/utils.ts';

	// The backend checks every service each 45 s (HEALTH_POLL_INTERVAL). A
	// reading older than two passes means the checks or the live stream
	// stopped, so the tile is dimmed instead of looking current.
	const STALE_AFTER_MS = 90_000;

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

	// Free space per mount. The backend caches for a minute, so a slow poll here
	// is enough — the numbers move in hours, not seconds.
	let mounts = $state<DiskMount[]>([]);
	$effect(() => {
		const load = async () => {
			try {
				mounts = await getDiskSpace();
			} catch {
				// leave the last reading up rather than blanking the card
			}
		};
		void load();
		const t = setInterval(load, 60000);
		return () => clearInterval(t);
	});

	let scanning = $state(false);
	let scanMsg = $state<{ text: string; error: boolean } | null>(null);
	let scanMsgTimer: ReturnType<typeof setTimeout>;
	async function doScan() {
		if (!(await confirmJellyfinScan())) return;
		scanning = true;
		scanMsg = null;
		try {
			await jellyfinScan();
			scanMsg = { text: 'Scan started', error: false };
		} catch {
			scanMsg = { text: 'Scan failed', error: true };
		} finally {
			scanning = false;
			clearTimeout(scanMsgTimer);
			scanMsgTimer = setTimeout(() => (scanMsg = null), 5000);
		}
	}

	type Tile = { health: ServiceHealth; kind?: string; label?: string; children: ServiceHealth[] };

	// Tiles derive from the registry (order, label, folding). Instances with a
	// parent_id (e.g. waha → notifyarr) are folded and not shown standalone.
	// Without /api/instances (older backend) every health row is a tile,
	// sorted by name.
	const tiles = $derived.by((): Tile[] => {
		if (live.instances.length > 0) {
			const out: Tile[] = [];
			for (const i of live.instances) {
				if (i.parent_id) continue;
				const health = live.services[i.id];
				if (!health) continue;
				const children = live.instances
					.filter((c) => c.parent_id === i.id)
					.map((c) => live.services[c.id])
					.filter((h): h is ServiceHealth => !!h);
				out.push({ health, kind: i.kind, label: i.label, children });
			}
			return out;
		}
		return Object.values(live.services)
			.sort((a, b) => a.service.localeCompare(b.service))
			.map((health) => ({ health, children: [] }));
	});

	function isStale(h: ServiceHealth): boolean {
		if (!live.connected) return true;
		const at = new Date(h.checked_at).getTime();
		return Number.isFinite(at) && clock.now - at > STALE_AFTER_MS;
	}
	const anyStale = $derived(tiles.some((t) => isStale(t.health)));
</script>

<div class="mb-6 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
	<div>
		<h1 class="text-xl font-semibold tracking-tight">Services</h1>
		<p class="text-sm text-muted-foreground">Live health of every tool in the pipeline.</p>
	</div>
	<div class="flex items-center gap-2">
		{#if scanMsg}
			<span role="status" class={cn('text-xs', scanMsg.error ? 'text-destructive' : 'text-muted-foreground')}>
				{scanMsg.text}
			</span>
		{/if}
		<button
			onclick={doScan}
			disabled={scanning}
			class="tap shrink-0 rounded-md border border-border px-3 py-1.5 text-xs font-medium hover:bg-accent disabled:opacity-50"
		>
			{scanning ? 'Scanning…' : 'Jellyfin scan'}
		</button>
	</div>
</div>

{#if live.servicesLoaded && (!live.connected || anyStale)}
	<div role="status" class="mb-4 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-xs text-warning">
		{#if !live.connected}
			Live updates are disconnected — showing the last known state. Reconnecting…
		{:else}
			Some checks are older than {STALE_AFTER_MS / 1000} s; their tiles are dimmed.
		{/if}
	</div>
{/if}

{#if !live.servicesLoaded}
	{#if live.servicesError}
		<div class="max-w-xl rounded-lg border border-dashed border-destructive/40 py-16 text-center">
			<div class="text-sm font-medium">Journarr's backend is not answering</div>
			<p class="mt-1 text-xs text-muted-foreground">Retrying in the background.</p>
		</div>
	{:else}
		<div class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3" aria-busy="true" aria-label="Loading services">
			{#each [0, 1, 2] as i (i)}
				<div class="h-28 animate-pulse rounded-lg border border-border bg-card"></div>
			{/each}
		</div>
	{/if}
{:else if tiles.length === 0}
	<div class="max-w-xl rounded-lg border border-dashed border-border py-16 text-center">
		<div class="text-sm font-medium">No services configured</div>
		<p class="mt-1 text-xs text-muted-foreground">
			Set SEERR_URL, SONARR_URL, … in the backend environment.
		</p>
	</div>
{:else}
	<div class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
		<DiskSpaceCard {mounts} />
		{#each tiles as t (t.health.service)}
			<ServiceCard
				service={t.health}
				kind={t.kind}
				label={t.label}
				children={t.children}
				stale={isStale(t.health)}
				streams={t.health.service === 'jellyfin' ? streams : undefined}
			/>
		{/each}
	</div>
{/if}
