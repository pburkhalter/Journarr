<script lang="ts">
	import { STAGES, stageIndex } from '$lib/types';
	import { cn } from '$lib/utils';

	let { stage, error = false }: { stage: string; error?: boolean } = $props();

	const label = $derived(STAGES.find((s) => s.key === stage)?.label ?? stage);
	const idx = $derived(stageIndex(stage));

	const color = $derived(
		error
			? 'bg-destructive/15 text-destructive'
			: stage === 'available' || stage === 'notified'
				? 'bg-success/15 text-success'
				: idx >= 2
					? 'bg-info/15 text-info'
					: 'bg-muted text-muted-foreground'
	);
</script>

<span class={cn('inline-flex items-center whitespace-nowrap rounded-full px-2 py-0.5 text-[11px] font-medium', color)}>
	{label}
</span>
