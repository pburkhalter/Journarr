<script lang="ts">
	import HeadroomMeter from '$lib/components/HeadroomMeter.svelte';
	import StatusDot from '$lib/components/StatusDot.svelte';
	import { parseDetail, type JellySession, type ServiceHealth, type TorboxCreate } from '$lib/types';
	import { cn, relativeTime, titleCase } from '$lib/utils';

	// `streams` = live Jellyfin now-playing (passed in for the Jellyfin tile only).
	let { service, label, streams }: { service: ServiceHealth; label?: string; streams?: JellySession[] } =
		$props();

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
	// WAHA status folded into the concierge tile (no standalone WAHA tile).
	const waha = $derived((detail['waha'] as string | undefined) ?? null);
	const headroom = $derived((detail['torbox_create'] as TorboxCreate | undefined) ?? null);
	const jobStates = $derived((detail['states'] as Record<string, number> | undefined) ?? null);
	const sessions = $derived(
		(detail['sessions'] as { name: string; status: string }[] | undefined) ?? null
	);
	const healthMessages = $derived((detail['health_messages'] as string[] | undefined) ?? null);
	const errorMsg = $derived((detail['error'] as string | undefined) ?? null);
	const serverName = $derived((detail['server_name'] as string | undefined) ?? null);
	// concierge extras
	const issues = $derived((detail['issues'] as string[] | undefined) ?? null);
	const grabQuota = $derived(
		(detail['grab_quota'] as { indexer: string; used: number; cap: number; left: number } | undefined) ?? null
	);
	const stuckJobs = $derived(detail['stuck_jobs'] as number | undefined);
	const unflushed = $derived(detail['unflushed'] as number | undefined);
	// tdarr: live transcode activity + whether a GPU worker is enabled
	const transcodes = $derived(
		(detail['transcodes'] as { file: string; percentage: number; fps: number; eta: string }[] | undefined) ??
			null
	);
	const gpuWorkers = $derived(detail['gpu_workers'] as number | undefined);

	const badge: Record<string, string> = {
		up: 'bg-success/15 text-success',
		degraded: 'bg-warning/15 text-warning',
		down: 'bg-destructive/15 text-destructive'
	};
</script>

<div class="rounded-lg border border-border bg-card p-4">
	<div class="flex items-start justify-between">
		<div class="flex items-center gap-2.5">
			<StatusDot status={service.status} />
			<div>
				<div class="text-sm font-medium leading-none">{label ?? titleCase(service.service)}</div>
				<div class="mt-1 text-[11px] text-muted-foreground">
					{#if version}v{version}{/if}
					{#if serverName}
						· {serverName}{/if}
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

	<div class="mt-3 flex items-center gap-3 text-[11px] text-muted-foreground">
		<span class="tabular-nums">{service.latency_ms} ms</span>
		<span>·</span>
		<span>checked {relativeTime(service.checked_at)}</span>
	</div>

	{#if errorMsg}
		<p class="mt-3 break-all rounded-md bg-destructive/10 px-2.5 py-1.5 text-[11px] text-destructive">
			{errorMsg}
		</p>
	{/if}

	{#if healthMessages && healthMessages.length > 0}
		<ul class="mt-3 space-y-1">
			{#each healthMessages as msg (msg)}
				<li class="rounded-md bg-warning/10 px-2.5 py-1.5 text-[11px] text-warning">{msg}</li>
			{/each}
		</ul>
	{/if}

	{#if issues && issues.length > 0}
		<ul class="mt-3 space-y-1">
			{#each issues as msg (msg)}
				<li class="rounded-md bg-warning/10 px-2.5 py-1.5 text-[11px] text-warning">{msg}</li>
			{/each}
		</ul>
	{/if}

	{#if grabQuota}
		<div class="mt-3">
			<div class="mb-1 flex items-center justify-between text-[11px] text-muted-foreground">
				<span>{grabQuota.indexer} grabs today</span>
				<span class="tabular-nums">{grabQuota.used}/{grabQuota.cap}</span>
			</div>
			<div class="h-1.5 overflow-hidden rounded-full bg-muted">
				<div
					class={cn('h-full rounded-full', grabQuota.left > grabQuota.cap * 0.15 ? 'bg-success' : 'bg-warning')}
					style="width: {Math.min(100, (grabQuota.used / grabQuota.cap) * 100)}%"
				></div>
			</div>
		</div>
	{/if}

	{#if stuckJobs !== undefined || unflushed !== undefined}
		<div class="mt-3 flex flex-wrap gap-1.5">
			{#if stuckJobs !== undefined}
				<span class="rounded-md bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
					stuck jobs <span class="font-medium text-foreground tabular-nums">{stuckJobs}</span>
				</span>
			{/if}
			{#if unflushed !== undefined}
				<span class="rounded-md bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
					unflushed <span class="font-medium text-foreground tabular-nums">{unflushed}</span>
				</span>
			{/if}
		</div>
	{/if}

	{#if jobStates && Object.keys(jobStates).length > 0}
		<div class="mt-3 flex flex-wrap gap-1.5">
			{#each Object.entries(jobStates) as [state, count] (state)}
				<span class="rounded-md bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
					{state} <span class="font-medium text-foreground tabular-nums">{count}</span>
				</span>
			{/each}
		</div>
	{/if}

	{#if headroom}
		<div class="mt-3">
			<HeadroomMeter {headroom} />
		</div>
	{/if}

	{#if sessions && sessions.length > 0}
		<div class="mt-3 flex flex-wrap gap-1.5">
			{#each sessions as s (s.name)}
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
				class={cn(
					'rounded-md px-2 py-0.5 text-[11px]',
					waha.toLowerCase() === 'working' || waha.toLowerCase() === 'up'
						? 'bg-success/15 text-success'
						: 'bg-warning/15 text-warning'
				)}
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
							<span class="text-muted-foreground">{s.paused ? '⏸' : '▶'}</span>
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

	{#if transcodes && transcodes.length > 0}
		<div class="mt-3 space-y-2 border-t border-border/60 pt-3">
			<div class="text-[11px] font-medium text-muted-foreground">Transcoding</div>
			{#each transcodes as t (t.file)}
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
	{:else if gpuWorkers === 0 && service.service === 'tdarr'}
		<div class="mt-3 border-t border-border/60 pt-3 text-[11px] text-warning">⚠ kein GPU-Worker aktiv — transkodiert nicht</div>
	{/if}
</div>
