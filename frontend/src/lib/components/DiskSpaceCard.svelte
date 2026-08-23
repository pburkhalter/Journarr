<script lang="ts">
	import type { DiskMount } from '$lib/types';
	import { cn, formatBytes } from '$lib/utils';

	let { mounts }: { mounts: DiskMount[] } = $props();

	// Container mounts like / and /config are reported too but are not what
	// anyone opens this card for. Show libraries by default, the rest on demand.
	let showAll = $state(false);
	const libraries = $derived(mounts.filter((m) => m.is_library));
	const others = $derived(mounts.filter((m) => !m.is_library));
	const visible = $derived(showAll || libraries.length === 0 ? mounts : libraries);

	// Thresholds are about what is left, not what is used: a 90%-full 20 TB pool
	// still has 2 TB free and is fine, while a 90%-full 100 GB one is not.
	function tone(m: DiskMount): string {
		const freeGiB = m.free_space / 1024 ** 3;
		if (freeGiB < 50 || m.used_percent >= 95) return 'bg-destructive';
		if (freeGiB < 200 || m.used_percent >= 85) return 'bg-warning';
		return 'bg-success';
	}
</script>

<div class="rounded-lg border border-border bg-card p-4">
	<div class="mb-3 flex items-baseline justify-between gap-2">
		<h2 class="text-sm font-semibold tracking-tight">Storage</h2>
		{#if others.length > 0 && libraries.length > 0}
			<button
				onclick={() => (showAll = !showAll)}
				class="text-[11px] text-muted-foreground hover:text-foreground"
			>
				{showAll ? 'Libraries only' : `All mounts (${mounts.length})`}
			</button>
		{/if}
	</div>

	{#if mounts.length === 0}
		<p class="py-6 text-center text-xs text-muted-foreground">
			No instance reports disk space yet.
		</p>
	{:else}
		<div class="flex flex-col gap-3">
			{#each visible as m (m.path + m.total_space)}
				<div>
					<div class="mb-1 flex items-baseline justify-between gap-2">
						<span class="truncate font-mono text-xs" title={m.reported_by.join(', ')}>
							{m.label || m.path}
						</span>
						<span class="shrink-0 tabular-nums text-xs">
							<span class="font-medium">{formatBytes(m.free_space)}</span>
							<span class="text-muted-foreground"> free</span>
						</span>
					</div>
					<div class="h-1.5 overflow-hidden rounded-full bg-muted">
						<div
							class={cn('h-full rounded-full transition-all', tone(m))}
							style="width: {Math.max(0, Math.min(100, m.used_percent))}%"
						></div>
					</div>
					<div class="mt-1 text-[11px] tabular-nums text-muted-foreground">
						{formatBytes(m.used_space)} of {formatBytes(m.total_space)} used
						({m.used_percent.toFixed(0)}%)
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>
