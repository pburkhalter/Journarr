<script lang="ts">
	import { STAGES, stageIndex, type Transition } from '$lib/types';
	import { cn } from '$lib/utils';

	let { transitions, currentCycle }: { transitions: Transition[]; currentCycle: number } = $props();

	const cycles = $derived.by(() => {
		const byCycle = new Map<number, Transition[]>();
		for (const t of transitions) {
			const list = byCycle.get(t.cycle) ?? [];
			list.push(t);
			byCycle.set(t.cycle, list);
		}
		return [...byCycle.entries()]
			.sort((a, b) => b[0] - a[0]) // newest cycle first
			.map(([cycle, ts]) => ({
				cycle,
				ts: ts.sort((a, b) => stageIndex(a.stage) - stageIndex(b.stage))
			}));
	});

	function fmt(iso: string): string {
		const d = new Date(iso);
		return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
	}

	function duration(prev: string, next: string): string {
		const ms = new Date(next).getTime() - new Date(prev).getTime();
		if (!Number.isFinite(ms) || ms < 0) return '';
		const s = Math.round(ms / 1000);
		if (s < 90) return `+${s}s`;
		const m = Math.round(s / 60);
		if (m < 90) return `+${m}m`;
		return `+${Math.round(m / 60)}h`;
	}
</script>

{#each cycles as { cycle, ts } (cycle)}
	<div class={cn('mb-3', cycle !== currentCycle && 'opacity-50')}>
		{#if cycles.length > 1}
			<div class="mb-1.5 text-[11px] font-medium text-muted-foreground">
				Attempt {cycle}{cycle === currentCycle ? ' (current)' : ''}
			</div>
		{/if}
		<ol class="relative border-l border-border pl-4">
			{#each ts as t, i (t.id)}
				<li class="relative pb-3 last:pb-0">
					<span class="absolute -left-[21px] top-1 size-2.5 rounded-full border-2 border-card bg-info"></span>
					<div class="flex items-baseline gap-2">
						<span class="text-xs font-medium">
							{STAGES.find((s) => s.key === t.stage)?.label ?? t.stage}
						</span>
						<span class="text-[11px] tabular-nums text-muted-foreground">{fmt(t.entered_at)}</span>
						{#if i > 0}
							<span class="text-[11px] text-muted-foreground/60">{duration(ts[i - 1].entered_at, t.entered_at)}</span>
						{/if}
					</div>
					{#if t.note}
						<div class="mt-0.5 break-all text-[11px] text-muted-foreground">{t.note}</div>
					{/if}
				</li>
			{/each}
		</ol>
	</div>
{/each}
