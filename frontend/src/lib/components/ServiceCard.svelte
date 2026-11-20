<script lang="ts">
	import HeadroomMeter from '#lib/components/HeadroomMeter.svelte';
	import StatusDot from '#lib/components/StatusDot.svelte';
	import { clock } from '#lib/clock.svelte.ts';
	import { parseDetail, type JellySession, type ServiceHealth } from '#lib/types.ts';
	import { cn, relativeTime, titleCase } from '#lib/utils.ts';

	// `kind` = registry kind (sonarr, arrarr, journarr, …); `streams` = live
	// Jellyfin now-playing (Jellyfin tile only); `children` = health of
	// instances folded into this tile (waha → notifyarr); `stale` = the
	// reading is too old to trust (set by the page).
	let {
		service,
		kind,
		label,
		streams,
		children = [],
		stale = false
	}: {
		service: ServiceHealth;
		kind?: string;
		label?: string;
		streams?: JellySession[];
		children?: ServiceHealth[];
		stale?: boolean;
	} = $props();

	function fmtDur(sec: number): string {
		const s = Math.max(0, Math.floor(sec));
		const h = Math.floor(s / 3600);
		const m = Math.floor((s % 3600) / 60);
		return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}` : `${m}:${String(s % 60).padStart(2, '0')}`;
	}

	// Normalize the reported version: some services (e.g. arrarr) already prefix
	// a "v", and we add our own — strip theirs so we never render "vv3.1.4".
	const version = $derived(service.version ? service.version.replace(/^v/i, '') : '');
	const detail = $derived(parseDetail(service));
	const isSelf = $derived((kind ?? service.service) === 'journarr');

	// Why the tile is not green, in words. Every check that can turn a tile
	// yellow or red leaves its reason in the detail; each one shows here.
	type Reason = { tone: 'error' | 'warn'; text: string };
	const reasons = $derived.by((): Reason[] => {
		const out: Reason[] = [];
		if (detail.error) out.push({ tone: 'error', text: detail.error });
		if (detail.health_error) out.push({ tone: 'warn', text: `Health check failed: ${detail.health_error}` });
		for (const m of detail.health_messages ?? []) out.push({ tone: 'warn', text: m });
		if (detail.poller) out.push({ tone: service.status === 'down' ? 'error' : 'warn', text: detail.poller });
		if (detail.restart_required) out.push({ tone: 'warn', text: 'Seerr asks for a restart to apply changed settings' });
		if (out.length === 0 && service.status !== 'up') {
			out.push({ tone: 'warn', text: `Reported ${service.status}, without a reason` });
		}
		return out;
	});

	// WhatsApp status from the folded WAHA instance (no standalone WAHA tile).
	const waha = $derived.by(() => {
		const w = children.find((c) => c.service === 'waha');
		if (!w) return null;
		return parseDetail(w).sessions?.[0]?.status ?? w.status;
	});
	const wahaOk = $derived(!!waha && ['working', 'up'].includes(waha.toLowerCase()));

	const dlBadge: Record<string, string> = {
		downloading: 'bg-info/15 text-info',
		importing: 'bg-primary/15 text-primary',
		queued: 'bg-muted text-muted-foreground'
	};
	const badge: Record<string, string> = {
		up: 'bg-success/15 text-success',
		degraded: 'bg-warning/15 text-warning',
		down: 'bg-destructive/15 text-destructive'
	};
	const chip = 'rounded-md bg-muted px-2 py-0.5 text-[11px] text-muted-foreground';
</script>

<div class={cn('rounded-lg border border-border bg-card p-4 transition-opacity', stale && 'opacity-55')}>
	<div class="flex items-start justify-between gap-3">
		<div class="flex min-w-0 items-center gap-2.5">
			<StatusDot status={service.status} />
			<div class="min-w-0">
				<div class="truncate text-sm font-medium leading-none">{label ?? titleCase(service.service)}</div>
				<div class="mt-1 truncate text-[11px] text-muted-foreground">
					{#if version}v{version}{/if}
					{#if detail.server_name}
						· {detail.server_name}{/if}
				</div>
			</div>
		</div>
		<div class="flex shrink-0 items-center gap-1.5">
			{#if service.update?.update_available}
				<span
					class="rounded-full bg-info/15 px-2 py-0.5 text-[11px] font-medium text-info"
					title="Update available: {service.update.current} → {service.update.latest}"
				>
					↑ {service.update.latest}
				</span>
			{/if}
			<span class={cn('rounded-full px-2 py-0.5 text-[11px] font-medium', badge[service.status])}>
				{service.status}
			</span>
		</div>
	</div>

	<div class="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
		{#if !isSelf && service.status !== 'down'}
			<span class="tabular-nums">{service.latency_ms} ms</span>
			<span aria-hidden="true">·</span>
		{/if}
		<span class={cn(stale && 'font-medium text-warning')}>
			checked {relativeTime(service.checked_at, clock.now)}{#if stale}&nbsp;· stale{/if}
		</span>
	</div>

	{#if reasons.length > 0}
		<ul class="mt-3 space-y-1">
			{#each reasons as r, i (i)}
				<li
					class={cn(
						'break-words rounded-md px-2.5 py-1.5 text-[11px]',
						r.tone === 'error' ? 'bg-destructive/10 text-destructive' : 'bg-warning/10 text-warning'
					)}
				>
					{r.text}
				</li>
			{/each}
		</ul>
	{/if}

	{#if detail.grab_quota}
		{@const q = detail.grab_quota}
		<div class="mt-3">
			<div class="mb-1 flex items-center justify-between text-[11px] text-muted-foreground">
				<span>{q.indexer} grabs today</span>
				<span class="tabular-nums">{q.used}/{q.cap}</span>
			</div>
			<div class="h-1.5 overflow-hidden rounded-full bg-muted">
				<div
					class={cn('h-full rounded-full', q.left > q.cap * 0.15 ? 'bg-success' : 'bg-warning')}
					style="width: {q.cap > 0 ? Math.min(100, (q.used / q.cap) * 100) : 0}%"
				></div>
			</div>
		</div>
	{/if}

	{#if detail.states && Object.keys(detail.states).length > 0}
		<div class="mt-3 flex flex-wrap gap-1.5">
			{#each Object.entries(detail.states) as [state, count] (state)}
				<span class={chip}>
					{state} <span class="font-medium text-foreground tabular-nums">{count}</span>
				</span>
			{/each}
		</div>
	{/if}

	{#if detail.torbox_create}
		<div class="mt-3">
			<HeadroomMeter headroom={detail.torbox_create} />
		</div>
	{/if}

	{#if detail.nodes !== undefined}
		<div class="mt-3 flex flex-wrap gap-1.5">
			<span class={chip}>nodes <span class="font-medium text-foreground tabular-nums">{detail.nodes}</span></span>
			{#if detail.gpu_workers !== undefined}
				<span class={chip}>
					GPU workers <span class="font-medium text-foreground tabular-nums">{detail.gpu_workers}</span>
				</span>
			{/if}
		</div>
	{/if}

	{#if detail.sessions && detail.sessions.length > 0}
		<div class="mt-3 flex flex-wrap gap-1.5">
			{#each detail.sessions as s (s.name)}
				<span
					class={cn(
						'rounded-md px-2 py-0.5 text-[11px]',
						s.status === 'WORKING' ? 'bg-success/15 text-success' : 'bg-warning/15 text-warning'
					)}
				>
					{s.name}: {s.status}
				</span>
			{/each}
		</div>
	{/if}

	{#if waha}
		<div class="mt-3 flex flex-wrap gap-1.5">
			<span
				class={cn('rounded-md px-2 py-0.5 text-[11px]', wahaOk ? 'bg-success/15 text-success' : 'bg-warning/15 text-warning')}
			>
				WhatsApp: {waha}
			</span>
		</div>
	{/if}

	{#if streams && streams.length > 0}
		<div class="mt-3 space-y-2 border-t border-border/60 pt-3">
			<div class="text-[11px] font-medium text-muted-foreground">Now Playing</div>
			{#each streams as s (s.user + s.device + s.title)}
				<div>
					<div class="flex items-start justify-between gap-2">
						<div class="min-w-0 text-[12px]">
							<span class="text-muted-foreground" aria-label={s.paused ? 'paused' : 'playing'}>{s.paused ? '⏸' : '▶'}</span>
							{s.title}
							<span class="text-[11px] text-muted-foreground">· {s.user} · {s.device || s.client}</span>
						</div>
						{#if s.play_method === 'Transcode'}
							<span class="shrink-0 rounded-full bg-warning/15 px-1.5 py-0.5 text-[10px] font-medium text-warning">⚙ Transcode</span>
						{/if}
					</div>
					{#if s.runtime_sec > 0}
						<div class="mt-1 flex items-center gap-2 text-[10px] text-muted-foreground">
							<div class="h-1 flex-1 overflow-hidden rounded-full bg-muted">
								<div class="h-full rounded-full bg-info" style="width: {Math.min(100, (s.position_sec / s.runtime_sec) * 100)}%"></div>
							</div>
							<span class="shrink-0 tabular-nums">{fmtDur(s.position_sec)} / {fmtDur(s.runtime_sec)}</span>
						</div>
					{/if}
				</div>
			{/each}
		</div>
	{/if}

	{#if detail.transcodes && detail.transcodes.length > 0}
		<div class="mt-3 space-y-2 border-t border-border/60 pt-3">
			<div class="text-[11px] font-medium text-muted-foreground">Transcoding</div>
			{#each detail.transcodes as t (t.file)}
				<div>
					<div class="truncate text-[12px]" title={t.file}>{t.file}</div>
					<div class="mt-1 flex items-center gap-2 text-[10px] text-muted-foreground">
						<div class="h-1 flex-1 overflow-hidden rounded-full bg-muted">
							<div class="h-full rounded-full bg-primary" style="width: {Math.min(100, t.percentage || 0)}%"></div>
						</div>
						<span class="shrink-0 tabular-nums">{Math.round(t.percentage || 0)}%{#if t.fps} · {Math.round(t.fps)} fps{/if}{#if t.eta} · {t.eta}{/if}</span>
					</div>
				</div>
			{/each}
		</div>
	{/if}

	{#if detail.downloads && detail.downloads.length > 0}
		{@const total = detail.download_count ?? detail.downloads.length}
		<div class="mt-3 space-y-2 border-t border-border/60 pt-3">
			<div class="flex items-center justify-between text-[11px] font-medium text-muted-foreground">
				<span>Downloads</span>
				{#if total > detail.downloads.length}
					<span class="font-normal">showing {detail.downloads.length} of {total}</span>
				{/if}
			</div>
			{#each detail.downloads as d, i (d.title + i)}
				<div class="flex items-start justify-between gap-2">
					<div class="min-w-0 truncate text-[12px]" title={d.title}>{d.title}</div>
					<span class={cn('shrink-0 rounded-full px-1.5 py-0.5 text-[10px] font-medium', dlBadge[d.state] ?? 'bg-muted text-muted-foreground')}>
						{d.state}
					</span>
				</div>
			{/each}
		</div>
	{/if}
</div>
