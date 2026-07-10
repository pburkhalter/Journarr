<script lang="ts">
	import { live } from '$lib/live.svelte';
	import { STAGES, type Transition } from '$lib/types';
	import { cn } from '$lib/utils';

	let {
		transitions,
		currentCycle,
		currentStage
	}: { transitions: Transition[]; currentCycle: number; currentStage: string } = $props();

	// Full active-stage skeleton, single-sourced from the backend (STAGES is the
	// pre-load fallback). Includes transcode only when a Tdarr instance exists.
	const stages = $derived(
		live.stages.length
			? live.stages.map((s) => ({ key: s.key, label: s.label }))
			: STAGES.map((s) => ({ key: s.key, label: s.label }))
	);
	const ordinalOf = (key: string) => stages.findIndex((s) => s.key === key);

	const lastKey = $derived(stages.length ? stages[stages.length - 1].key : 'notified');
	const isComplete = $derived(
		currentStage === lastKey || currentStage === 'available' || currentStage === 'notified'
	);

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

	type Node = {
		key: string;
		label: string;
		state: 'done' | 'current' | 'skipped' | 'pending';
		at?: string;
		note?: string;
		delta: string;
	};

	// The full skeleton for the current cycle: every active stage marked
	// passed / current / skipped / pending, so it's clear what's done vs. left.
	const currentNodes = $derived.by((): Node[] => {
		const byStage = new Map<string, Transition>();
		for (const t of transitions) if (t.cycle === currentCycle) byStage.set(t.stage, t);
		const curIdx = ordinalOf(currentStage);
		let prevAt: string | null = null;
		return stages.map((s, i): Node => {
			const tr = byStage.get(s.key);
			let state: Node['state'];
			if (s.key === currentStage) state = 'current';
			else if (tr) state = 'done';
			else if (curIdx >= 0 && i < curIdx) state = 'skipped';
			else state = 'pending';
			const at = tr?.entered_at;
			const delta = at && prevAt ? duration(prevAt, at) : '';
			if (at) prevAt = at;
			return { key: s.key, label: s.label, state, at, note: tr?.note, delta };
		});
	});

	// Prior attempts: only what actually happened, dimmed.
	const olderCycles = $derived.by(() => {
		const byCycle = new Map<number, Transition[]>();
		for (const t of transitions) {
			if (t.cycle === currentCycle) continue;
			const list = byCycle.get(t.cycle) ?? [];
			list.push(t);
			byCycle.set(t.cycle, list);
		}
		return [...byCycle.entries()]
			.sort((a, b) => b[0] - a[0])
			.map(([cycle, ts]) => ({ cycle, ts: ts.sort((a, b) => ordinalOf(a.stage) - ordinalOf(b.stage)) }));
	});

	function dotClass(n: Node): string {
		if (n.state === 'pending') return 'border-2 border-border bg-card';
		if (n.state === 'skipped') return 'border-2 border-dashed border-muted-foreground/40 bg-card';
		const base =
			n.key === 'available' || n.key === 'notified'
				? 'bg-success'
				: 'bg-info';
		return cn(base, n.state === 'current' && !isComplete && 'ring-2 ring-info/40 animate-pulse');
	}
</script>

{#if olderCycles.length > 0}
	<div class="mb-1.5 text-[11px] font-medium text-muted-foreground">Attempt {currentCycle} (current)</div>
{/if}
<ol class="relative border-l border-border pl-4">
	{#each currentNodes as n (n.key)}
		<li class="relative pb-3 last:pb-0">
			<span class={cn('absolute -left-[21px] top-1 size-2.5 rounded-full', dotClass(n))}></span>
			<div class="flex items-baseline gap-2">
				<span
					class={cn(
						'text-xs',
						n.state === 'current' ? 'font-semibold' : n.state === 'done' ? 'font-medium' : 'text-muted-foreground'
					)}
				>
					{n.label}
				</span>
				{#if n.at}
					<span class="text-[11px] tabular-nums text-muted-foreground">{fmt(n.at)}</span>
					{#if n.delta}<span class="text-[11px] text-muted-foreground/60">{n.delta}</span>{/if}
					{#if n.state === 'current' && !isComplete}
						<span class="text-[11px] font-medium text-info">· in progress</span>
					{/if}
				{:else if n.state === 'skipped'}
					<span class="text-[11px] text-muted-foreground/50">skipped</span>
				{:else}
					<span class="text-[11px] text-muted-foreground/40">pending</span>
				{/if}
			</div>
			{#if n.note}
				<div class="mt-0.5 break-all text-[11px] text-muted-foreground">{n.note}</div>
			{/if}
		</li>
	{/each}
</ol>

{#each olderCycles as { cycle, ts } (cycle)}
	<div class="mt-3 opacity-50">
		<div class="mb-1.5 text-[11px] font-medium text-muted-foreground">Attempt {cycle}</div>
		<ol class="relative border-l border-border pl-4">
			{#each ts as t, i (t.id)}
				<li class="relative pb-3 last:pb-0">
					<span class="absolute -left-[21px] top-1 size-2.5 rounded-full border-2 border-card bg-info"></span>
					<div class="flex items-baseline gap-2">
						<span class="text-xs font-medium">{stages.find((s) => s.key === t.stage)?.label ?? t.stage}</span>
						<span class="text-[11px] tabular-nums text-muted-foreground">{fmt(t.entered_at)}</span>
						{#if i > 0}<span class="text-[11px] text-muted-foreground/60">{duration(ts[i - 1].entered_at, t.entered_at)}</span>{/if}
					</div>
					{#if t.note}<div class="mt-0.5 break-all text-[11px] text-muted-foreground">{t.note}</div>{/if}
				</li>
			{/each}
		</ol>
	</div>
{/each}
