<script lang="ts">
	import { getSessions } from '$lib/api';
	import type { SessionsResponse } from '$lib/types';
	import { cn } from '$lib/utils';

	let data = $state<SessionsResponse | null>(null);

	async function load() {
		try {
			data = await getSessions();
		} catch {
			// jellyfin unreachable — leave last state; the services grid shows it
		}
	}

	// Poll every 8s while mounted (progress + session set change often).
	$effect(() => {
		void load();
		const t = setInterval(load, 8000);
		return () => clearInterval(t);
	});

	function fmt(sec: number): string {
		const s = Math.max(0, Math.floor(sec));
		const h = Math.floor(s / 3600);
		const m = Math.floor((s % 3600) / 60);
		const ss = String(s % 60).padStart(2, '0');
		return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`;
	}

	const methodBadge: Record<string, string> = {
		Transcode: 'bg-warning/15 text-warning',
		DirectPlay: 'bg-success/15 text-success',
		DirectStream: 'bg-info/15 text-info'
	};
</script>

{#if data}
	<div class="mb-4 rounded-lg border border-border bg-card p-4">
		<div class="flex flex-wrap items-center justify-between gap-2">
			<div class="text-sm font-medium">Now Playing</div>
			{#if data.safe_to_restart}
				<span class="rounded-full bg-success/15 px-2.5 py-0.5 text-[11px] font-medium text-success">
					✓ niemand streamt — gefahrlos neustartbar
				</span>
			{:else}
				<span class="rounded-full bg-warning/15 px-2.5 py-0.5 text-[11px] font-medium text-warning">
					⚠ {data.sessions.length} aktive{data.sessions.length === 1 ? 'r' : ''} Stream{data.sessions.length === 1 ? '' : 's'} — Neustart unterbricht
				</span>
			{/if}
		</div>

		{#if data.sessions.length > 0}
			<div class="mt-3 space-y-2">
				{#each data.sessions as s (s.user + s.device + s.title)}
					<div class="rounded-md border border-border/60 bg-muted/30 p-2.5">
						<div class="flex items-start justify-between gap-2">
							<div class="min-w-0">
								<div class="truncate text-sm">
									<span class="text-muted-foreground">{s.paused ? '⏸' : '▶'}</span>
									{s.title}
								</div>
								<div class="mt-0.5 text-[11px] text-muted-foreground">
									{s.user} · {s.device || s.client}{#if s.remote_ip} · {s.remote_ip}{/if}
								</div>
							</div>
							<span
								class={cn(
									'shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium',
									methodBadge[s.play_method] ?? 'bg-muted text-muted-foreground'
								)}
								title={s.play_method}
							>
								{s.play_method === 'Transcode' ? '⚙ Transcode' : 'Direct'}
							</span>
						</div>
						{#if s.runtime_sec > 0}
							<div class="mt-2 flex items-center gap-2 text-[11px] text-muted-foreground">
								<div class="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
									<div
										class="h-full rounded-full bg-info"
										style="width: {Math.min(100, (s.position_sec / s.runtime_sec) * 100)}%"
									></div>
								</div>
								<span class="shrink-0 tabular-nums">{fmt(s.position_sec)} / {fmt(s.runtime_sec)}</span>
							</div>
						{/if}
					</div>
				{/each}
			</div>
		{/if}
	</div>
{/if}
