<script lang="ts">
	import type { RemovalPlan } from '#lib/types.ts';
	import { cn, formatBytes, titleCase } from '#lib/utils.ts';

	// What "Remove everywhere" would delete, from GET /api/requests/{id}/removal.
	// Display only — the caller fetches the plan and asks for confirmation.
	// Shared by the request page and the approvals page.
	let { plan }: { plan: RemovalPlan } = $props();

	// plan.arr names the arr the title lives in; without it (not found there)
	// the media type says which one was searched.
	const arrName = $derived(plan.arr ? titleCase(plan.arr) : plan.media_type === 'tv' ? 'Sonarr' : 'Radarr');
	const what = $derived(
		plan.media_type === 'tv'
			? `${plan.files} episode file(s) in ${plan.seasons ?? 0} season(s)`
			: plan.files
				? '1 movie file'
				: 'no file yet'
	);
	// recycle_days 0 is the arrs' "never clean up", not "unknown".
	const bin = $derived(
		!plan.recycle_bin
			? 'deleted for good — no recycle bin set'
			: plan.recycle_days > 0
				? `moved to the recycle bin, emptied after ${plan.recycle_days} days`
				: 'moved to the recycle bin, never emptied automatically'
	);

	type Line = { text: string; tone?: 'warn' | 'danger'; mono?: boolean };
	const lines = $derived.by((): Line[] => {
		const out: Line[] = [];
		if (plan.arr_id) {
			out.push({
				text: `${arrName}: ${plan.media_type === 'tv' ? 'series' : 'movie'} and ${what}, ${formatBytes(plan.size_bytes)} — ${bin}`,
				tone: plan.recycle_bin ? undefined : 'danger'
			});
			if (plan.path) out.push({ text: `Folder: ${plan.path}`, mono: true });
		} else {
			out.push({ text: `Not in ${arrName}` });
		}
		if (plan.downloads.length) {
			out.push({ text: `Running downloads stopped and deleted: ${plan.downloads.join(', ')}` });
		}
		out.push({ text: plan.in_seerr ? 'Seerr: request(s) removed, the title can be requested again' : 'Not in Seerr' });
		out.push({ text: `Journarr: ${plan.requests} request(s) and their history` });
		for (const w of plan.warnings) out.push({ text: w, tone: 'warn' });
		if (plan.viewers.length) out.push({ text: `Watching now: ${plan.viewers.join(', ')}`, tone: 'danger' });
		return out;
	});
</script>

<ul class="space-y-1.5 text-xs">
	{#each lines as l, i (i)}
		<li
			class={cn(
				'flex gap-1.5 break-words',
				l.tone === 'warn' && 'text-warning',
				l.tone === 'danger' && 'text-destructive'
			)}
		>
			<span class="text-muted-foreground" aria-hidden="true">{l.tone ? '⚠' : '•'}</span>
			<span class={cn('min-w-0', l.mono && 'break-all font-mono text-[11px]')}>{l.text}</span>
		</li>
	{/each}
</ul>
