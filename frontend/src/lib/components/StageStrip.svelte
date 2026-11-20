<script lang="ts">
	import { live } from '$lib/live.svelte';
	import { cn } from '$lib/utils';

	/**
	 * Aggregated progress strip for a request: one segment per active
	 * pipeline stage (live catalog, so transcode shows when Tdarr is on),
	 * filled proportionally to how many items have reached it.
	 */
	let { counts, total }: { counts: Record<string, number>; total: number } = $props();

	const stages = $derived(live.stageList);
	const availableIdx = $derived(live.stageIndex('available'));

	// For each stage: fraction of items at-or-beyond it.
	const fills = $derived(
		stages.map((_, i) => {
			if (total === 0) return 0;
			let atOrBeyond = 0;
			for (const [stage, n] of Object.entries(counts)) {
				if (live.stageIndex(stage) >= i) atOrBeyond += n;
			}
			return atOrBeyond / total;
		})
	);

	const summary = $derived(
		stages.map((s, i) => `${s.label}: ${Math.round(fills[i] * 100)}%`).join(' · ')
	);
</script>

<div class="flex w-full gap-0.5" role="img" aria-label={summary} title={summary}>
	{#each stages as s, i (s.key)}
		<div class="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
			<div
				class={cn(
					'h-full rounded-full transition-all duration-500',
					availableIdx >= 0 && i >= availableIdx ? 'bg-success' : 'bg-info'
				)}
				style="width: {fills[i] * 100}%"
			></div>
		</div>
	{/each}
</div>
