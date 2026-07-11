<script lang="ts">
	import StageStrip from '$lib/components/StageStrip.svelte';
	import { stageIndex, type RequestRollup } from '$lib/types';
	import { cn, relativeTime } from '$lib/utils';

	let { request }: { request: RequestRollup } = $props();

	const doneCount = $derived.by(() => {
		let n = 0;
		for (const [stage, c] of Object.entries(request.stage_counts ?? {})) {
			if (stageIndex(stage) >= stageIndex('available')) n += c;
		}
		return n;
	});

	// Posters come from Seerr and are always TMDB-hosted; anything else in
	// the DB (crafted release metadata) is not worth a browser request.
	const safePoster = $derived(
		request.poster_url?.startsWith('https://image.tmdb.org/') ? request.poster_url : null
	);

	const statusBadge: Record<string, string> = {
		active: 'bg-info/15 text-info',
		completed: 'bg-success/15 text-success',
		partial: 'bg-warning/15 text-warning',
		failed: 'bg-destructive/15 text-destructive',
		canceled: 'bg-muted text-muted-foreground'
	};

	// A movie requested before its release date: Radarr can't grab it yet, so
	// show "waiting for release" (with the expected month) instead of a stall.
	// A far-future sentinel year means Radarr has no date yet (TBA).
	const awaitingDate = $derived(
		request.awaiting_release_at ? new Date(request.awaiting_release_at) : null
	);
	const awaiting = $derived(!!awaitingDate);
	const awaitingLabel = $derived(
		!awaitingDate || awaitingDate.getUTCFullYear() >= 9000
			? 'date TBA'
			: awaitingDate.toLocaleDateString(undefined, { month: 'short', year: 'numeric' })
	);
	// Movies are literally unreleased; a series is usually awaiting its next
	// episode/season, so word the badge accordingly.
	const awaitingText = $derived(
		request.media_type === 'tv' ? 'waiting for next episode' : 'waiting for release'
	);
</script>

<a
	href="/requests/{request.id}"
	class="group flex min-w-0 gap-3.5 rounded-lg border border-border bg-card p-3.5 transition-colors hover:border-ring/40"
>
	{#if safePoster}
		<img
			src={safePoster}
			alt=""
			class="h-24 w-16 shrink-0 rounded-md object-cover bg-muted"
			loading="lazy"
		/>
	{:else}
		<div class="flex h-24 w-16 shrink-0 items-center justify-center rounded-md bg-muted text-lg text-muted-foreground">
			{request.media_type === 'tv' ? '📺' : '🎬'}
		</div>
	{/if}

	<div class="min-w-0 flex-1">
		<div class="flex items-start justify-between gap-2">
			<div class="min-w-0">
				<div class="truncate text-sm font-medium group-hover:text-foreground">
					{request.title || '(untitled)'}
					{#if request.year}<span class="text-muted-foreground"> ({request.year})</span>{/if}
				</div>
				<div class="mt-0.5 text-[11px] text-muted-foreground">
					{#if request.requested_by}{request.requested_by} · {/if}
					{#if request.seerr_request_id == null}manual · {/if}
					{relativeTime(request.updated_at)}
				</div>
			</div>
			<div class="flex shrink-0 items-center gap-1.5">
				{#if awaiting}
					<span
						class="rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground"
						title="Not out yet — expected {awaitingLabel}"
					>
						🕗 {awaitingText}
					</span>
				{:else}
					{#if request.stuck_count > 0}
						<span class="rounded-full bg-warning/15 px-2 py-0.5 text-[11px] font-medium text-warning" title="{request.stuck_count} item(s) stuck">
							⏳ {request.stuck_count}
						</span>
					{/if}
					<span class={cn('rounded-full px-2 py-0.5 text-[11px] font-medium', statusBadge[request.status])}>
						{request.status}
					</span>
				{/if}
			</div>
		</div>

		<div class="mt-3">
			<StageStrip counts={request.stage_counts ?? {}} total={request.item_count} />
		</div>
		<div class="mt-1.5 flex items-center justify-between text-[11px] text-muted-foreground">
			<span>
				{#if awaiting}
					expected {awaitingLabel}
				{:else if request.media_type === 'tv'}
					{doneCount}/{request.item_count} episodes available
				{:else}
					{doneCount === 1 ? 'available' : 'in progress'}
				{/if}
			</span>
			{#if request.last_error}
				<span class="truncate pl-2 text-destructive" title={request.last_error}>⚠ {request.last_error}</span>
			{/if}
		</div>
	</div>
</a>
