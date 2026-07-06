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
			<span class={cn('shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium', statusBadge[request.status])}>
				{request.status}
			</span>
		</div>

		<div class="mt-3">
			<StageStrip counts={request.stage_counts ?? {}} total={request.item_count} />
		</div>
		<div class="mt-1.5 flex items-center justify-between text-[11px] text-muted-foreground">
			<span>
				{#if request.media_type === 'tv'}
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
