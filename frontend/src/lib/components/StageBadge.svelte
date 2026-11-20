<script lang="ts">
	import { live } from '#lib/live.svelte.ts';
	import { cn } from '#lib/utils.ts';

	let { stage, error = false }: { stage: string; error?: boolean } = $props();

	const label = $derived(live.stageLabel(stage));
	const idx = $derived(live.stageIndex(stage));
	const grabbedIdx = $derived(live.stageIndex('grabbed'));

	const color = $derived(
		error
			? 'bg-destructive/15 text-destructive'
			: stage === 'available' || stage === 'notified'
				? 'bg-success/15 text-success'
				: grabbedIdx >= 0 && idx >= grabbedIdx
					? 'bg-info/15 text-info'
					: 'bg-muted text-muted-foreground'
	);
</script>

<span class={cn('inline-flex items-center whitespace-nowrap rounded-full px-2 py-0.5 text-[11px] font-medium', color)}>
	{label}
</span>
