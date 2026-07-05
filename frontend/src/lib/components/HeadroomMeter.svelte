<script lang="ts">
	import type { TorboxCreate } from '$lib/types';
	import { cn } from '$lib/utils';

	let { headroom }: { headroom: TorboxCreate } = $props();

	const pct = $derived(
		headroom.capacity > 0 ? Math.max(0, Math.min(100, (headroom.available / headroom.capacity) * 100)) : 0
	);
	const barColor = $derived(pct > 40 ? 'bg-success' : pct > 15 ? 'bg-warning' : 'bg-destructive');
</script>

<div>
	<div class="mb-1 flex items-center justify-between text-[11px] text-muted-foreground">
		<span>TorBox create headroom</span>
		<span class="tabular-nums">{headroom.available}/{headroom.capacity}</span>
	</div>
	<div class="h-1.5 overflow-hidden rounded-full bg-muted">
		<div class={cn('h-full rounded-full transition-all', barColor)} style="width: {pct}%"></div>
	</div>
</div>
