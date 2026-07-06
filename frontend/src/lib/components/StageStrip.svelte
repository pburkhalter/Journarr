<script lang="ts">
	import { STAGES, stageIndex } from '$lib/types';
	import { cn } from '$lib/utils';

	/**
	 * Aggregated progress strip for a request: one segment per pipeline
	 * stage, filled proportionally to how many items have reached it.
	 */
	let { counts, total }: { counts: Record<string, number>; total: number } = $props();

	// For each stage: fraction of items at-or-beyond it.
	const fills = $derived(
		STAGES.map((s, i) => {
			if (total === 0) return 0;
			let atOrBeyond = 0;
			for (const [stage, n] of Object.entries(counts)) {
				if (stageIndex(stage) >= i) atOrBeyond += n;
			}
			return atOrBeyond / total;
		})
	);

	const segColor = (i: number) =>
		i >= stageIndex('available') ? 'bg-success' : i >= stageIndex('imported') ? 'bg-primary' : 'bg-info';
</script>

<div class="flex w-full gap-0.5" title={STAGES.map((s) => `${s.label}: ${Math.round(fills[stageIndex(s.key)] * 100)}%`).join(' · ')}>
	{#each STAGES as s, i (s.key)}
		<div class="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
			<div
				class={cn('h-full rounded-full transition-all duration-500', segColor(i))}
				style="width: {fills[i] * 100}%"
			></div>
		</div>
	{/each}
</div>
