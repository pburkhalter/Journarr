<script lang="ts">
	import HeadroomMeter from '$lib/components/HeadroomMeter.svelte';
	import StatusDot from '$lib/components/StatusDot.svelte';
	import { parseDetail, type ServiceHealth, type TorboxCreate } from '$lib/types';
	import { cn, relativeTime, titleCase } from '$lib/utils';

	let { service }: { service: ServiceHealth } = $props();

	const detail = $derived(parseDetail(service));
	const headroom = $derived((detail['torbox_create'] as TorboxCreate | undefined) ?? null);
	const jobStates = $derived((detail['states'] as Record<string, number> | undefined) ?? null);
	const sessions = $derived(
		(detail['sessions'] as { name: string; status: string }[] | undefined) ?? null
	);
	const healthMessages = $derived((detail['health_messages'] as string[] | undefined) ?? null);
	const errorMsg = $derived((detail['error'] as string | undefined) ?? null);
	const serverName = $derived((detail['server_name'] as string | undefined) ?? null);

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
				<div class="text-sm font-medium leading-none">{titleCase(service.service)}</div>
				<div class="mt-1 text-[11px] text-muted-foreground">
					{#if service.version}v{service.version}{/if}
					{#if serverName}
						· {serverName}{/if}
				</div>
			</div>
		</div>
		<span class={cn('rounded-full px-2 py-0.5 text-[11px] font-medium', badge[service.status])}>
			{service.status}
		</span>
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
</div>
