<script lang="ts">
	import type { ServiceStatus } from '$lib/types';
	import { cn } from '$lib/utils';

	let { status }: { status: ServiceStatus } = $props();

	const color: Record<ServiceStatus, string> = {
		up: 'bg-success',
		degraded: 'bg-warning',
		down: 'bg-destructive'
	};
</script>

<span class="relative flex size-2.5">
	{#if status !== 'up'}
		<span
			class={cn('absolute inline-flex h-full w-full animate-ping rounded-full opacity-60', color[status])}
		></span>
	{/if}
	<span class={cn('relative inline-flex size-2.5 rounded-full', color[status])}></span>
</span>
