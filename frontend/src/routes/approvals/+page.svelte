<script lang="ts">
	import { page } from '$app/state';
	import { decideApproval, getApproval, getApprovals } from '$lib/api';
	import { clock } from '$lib/clock.svelte';
	import RemovalPlanView from '$lib/components/RemovalPlanView.svelte';
	import { confirm } from '$lib/confirm.svelte';
	import { live } from '$lib/live.svelte';
	import type { Approval, ApprovalDetail, RemovalPlan } from '$lib/types';
	import { cn, formatTime, relativeTime, timeUntil } from '$lib/utils';

	// Removal requests MCP clients filed. Approving deletes the title
	// everywhere — the server accepts it only from an SSO session and only if
	// the title still matches the plan the request was made with.

	let approvals = $state<Approval[]>([]);
	let details = $state<Record<number, ApprovalDetail>>({});
	let enabled = $state(true);
	let loaded = $state(false);
	let loadError = $state(false);
	let busy = $state<Record<number, boolean>>({});
	let force = $state<Record<number, boolean>>({});
	let flash = $state<Record<number, { tone: 'info' | 'error'; text: string }>>({});

	const pending = $derived(approvals.filter((a) => a.status === 'pending'));
	const decided = $derived(approvals.filter((a) => a.status !== 'pending'));
	// Only a person with an SSO session may decide; the API token is refused
	// by the server, so its buttons stay disabled here too.
	const canDecide = $derived(!!live.me?.auth_enabled && !!live.me.user && live.me.user.sub !== 'api-token');
	const focusId = $derived(Number(page.url.searchParams.get('id')) || 0);

	async function load() {
		try {
			const r = await getApprovals();
			approvals = r.approvals;
			enabled = r.enabled;
			loadError = false;
			const next: Record<number, ApprovalDetail> = {};
			await Promise.all(
				r.approvals
					.filter((a) => a.status === 'pending')
					.map(async (a) => {
						try {
							next[a.id] = await getApproval(a.id);
						} catch {
							// the card falls back to the stored plan
						}
					})
			);
			details = next;
		} catch {
			loadError = true;
		} finally {
			loaded = true;
		}
	}

	$effect(() => {
		void live.approvalTick;
		void load();
	});

	$effect(() => {
		if (!loaded || !focusId) return;
		document.getElementById(`approval-${focusId}`)?.scrollIntoView({ block: 'start', behavior: 'smooth' });
	});

	// the plan shown in the confirm dialog (snippets passed to confirm.ask
	// take no arguments)
	let confirmPlan = $state<RemovalPlan | null>(null);

	function planOf(a: Approval): RemovalPlan {
		return details[a.id]?.current_plan ?? a.plan;
	}

	async function approve(a: Approval) {
		const plan = planOf(a);
		confirmPlan = plan;
		const watching = plan.viewers.length > 0;
		const ok = await confirm.ask({
			title: `Remove "${a.title}" everywhere?`,
			message: `${a.requested_by} asked for this${a.reason ? `: "${a.reason}"` : '.'} It deletes the title with its files from every system.`,
			body: planBody,
			confirmLabel: watching && force[a.id] ? 'Remove anyway' : 'Remove',
			danger: true
		});
		if (!ok) return;
		await decide(a, 'approve');
	}

	async function reject(a: Approval) {
		await decide(a, 'reject');
	}

	async function decide(a: Approval, decision: 'approve' | 'reject') {
		busy[a.id] = true;
		flash[a.id] = { tone: 'info', text: decision === 'approve' ? 'Removing…' : 'Rejecting…' };
		try {
			const res = await decideApproval(a.id, decision, !!force[a.id]);
			if (res.ok) {
				flash[a.id] = { tone: 'info', text: decision === 'approve' ? 'Removed.' : 'Rejected — nothing was deleted.' };
			} else {
				const why: Record<string, string> = {
					watching: 'Someone is watching it — nothing was removed. Tick "even while someone is watching" to override.',
					plan_changed: 'The title changed since the request — nothing was removed. Ask for a new request.',
					not_pending: 'This request was already decided or has expired.'
				};
				flash[a.id] = { tone: 'error', text: why[res.conflict ?? ''] ?? `Failed: ${res.error}` };
			}
		} catch {
			flash[a.id] = { tone: 'error', text: 'Could not reach Journarr.' };
		} finally {
			busy[a.id] = false;
			await load();
			void live.loadApprovals();
		}
	}

	const statusTone: Record<Approval['status'], string> = {
		pending: 'bg-warning/15 text-warning',
		approved: 'bg-info/15 text-info',
		executed: 'bg-success/15 text-success',
		rejected: 'bg-muted text-muted-foreground',
		expired: 'bg-muted text-muted-foreground',
		failed: 'bg-destructive/15 text-destructive'
	};
	const btn = 'tap rounded-md px-3 py-1.5 text-sm font-medium disabled:opacity-50';
</script>

{#snippet planBody()}
	{#if confirmPlan}<RemovalPlanView plan={confirmPlan} />{/if}
{/snippet}

<div class="mb-5">
	<h1 class="text-lg font-semibold tracking-tight">Approvals</h1>
	<p class="mt-1 text-sm text-muted-foreground">
		Removals an AI assistant asked for. Nothing is deleted until you approve it here.
	</p>
</div>

{#if !loaded}
	<p class="text-sm text-muted-foreground">Loading…</p>
{:else if loadError}
	<p class="text-sm text-muted-foreground">
		Couldn't load the requests. <button class="underline" onclick={load}>Retry</button>
	</p>
{:else}
	{#if !enabled}
		<p class="mb-4 rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground">
			Removal requests are off (JOURNARR_MCP_REMOVE). Older requests stay listed.
		</p>
	{/if}
	{#if !canDecide && pending.length}
		<p class="mb-4 rounded-md bg-warning/10 px-3 py-2 text-sm text-warning">
			Approving needs a login through SSO — tokens and open LAN mode cannot approve.
		</p>
	{/if}

	<section aria-labelledby="pending-h">
		<h2 id="pending-h" class="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
			Waiting ({pending.length})
		</h2>
		{#if pending.length === 0}
			<p class="mb-6 text-sm text-muted-foreground">Nothing is waiting for you.</p>
		{/if}
		<div class="mb-8 space-y-3">
			{#each pending as a (a.id)}
				{@const d = details[a.id]}
				{@const plan = planOf(a)}
				<article
					id="approval-{a.id}"
					class={cn('rounded-lg border bg-card p-4', a.id === focusId ? 'border-warning' : 'border-border')}
				>
					<div class="flex flex-wrap items-start justify-between gap-2">
						<div class="min-w-0">
							<h3 class="break-words text-base font-semibold">{a.title}</h3>
							<p class="mt-0.5 text-xs text-muted-foreground">
								asked by <span class="font-medium text-foreground">{a.requested_by}</span>
								· {relativeTime(a.created_at, clock.now)} · expires {timeUntil(a.expires_at, clock.now)}
							</p>
						</div>
						<span class={cn('rounded-full px-2 py-0.5 text-xs font-medium', statusTone[a.status])}>waiting</span>
					</div>
					{#if a.reason}
						<blockquote class="mt-3 border-l-2 border-border pl-3 text-sm text-foreground">{a.reason}</blockquote>
					{/if}
					{#if d?.plan_changed}
						<p class="mt-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
							The title changed since this request (other entry, folder, requests or Seerr entries). Approving would
							remove nothing — reject it and ask for a new request.
						</p>
					{:else if d?.current_plan_error}
						<p class="mt-3 rounded-md bg-warning/10 px-3 py-2 text-sm text-warning">
							Current state unknown: {d.current_plan_error}. Showing the plan from the time of the request.
						</p>
					{/if}
					<div class="mt-3 rounded-md bg-muted/40 p-3">
						<RemovalPlanView {plan} />
					</div>
					{#if plan.viewers.length}
						<label class="tap mt-3 flex items-center gap-2 text-sm">
							<input type="checkbox" bind:checked={force[a.id]} class="size-4" />
							even while someone is watching
						</label>
					{/if}
					{#if flash[a.id]}
						<p
							role={flash[a.id].tone === 'error' ? 'alert' : 'status'}
							class={cn(
								'mt-3 rounded-md px-3 py-1.5 text-sm',
								flash[a.id].tone === 'error' ? 'bg-destructive/10 text-destructive' : 'bg-muted'
							)}
						>
							{flash[a.id].text}
						</p>
					{/if}
					<div class="mt-4 flex flex-wrap gap-2">
						<button
							class={cn(btn, 'bg-destructive text-background hover:bg-destructive/90')}
							disabled={!canDecide || busy[a.id] || !!d?.plan_changed}
							onclick={() => approve(a)}
						>
							Remove
						</button>
						<button
							class={cn(btn, 'border border-border hover:bg-accent')}
							disabled={!canDecide || busy[a.id]}
							onclick={() => reject(a)}
						>
							Reject
						</button>
					</div>
				</article>
			{/each}
		</div>
	</section>

	{#if decided.length}
		<section aria-labelledby="decided-h">
			<h2 id="decided-h" class="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">Decided</h2>
			<ul class="divide-y divide-border rounded-lg border border-border bg-card">
				{#each decided as a (a.id)}
					<li id="approval-{a.id}" class="px-4 py-3 text-sm">
						<div class="flex flex-wrap items-center justify-between gap-2">
							<span class="min-w-0 break-words font-medium">{a.title}</span>
							<span class={cn('rounded-full px-2 py-0.5 text-xs font-medium', statusTone[a.status])}>{a.status}</span>
						</div>
						<p class="mt-0.5 text-xs text-muted-foreground">
							asked by {a.requested_by} · {formatTime(a.created_at)}
							{#if a.decided_by}· {a.status === 'rejected' ? 'rejected' : 'approved'} by {a.decided_by}{/if}
							{#if a.decided_at}· {relativeTime(a.decided_at, clock.now)}{/if}
						</p>
						{#if a.result?.error}
							<p class="mt-1 break-words text-xs text-destructive">{a.result.error}</p>
						{:else if a.result?.steps?.length}
							<p class="mt-1 break-words text-xs text-muted-foreground">
								{a.result.steps.map((s) => `${s.step}: ${s.status}`).join(' · ')}
							</p>
						{/if}
					</li>
				{/each}
			</ul>
		</section>
	{/if}
{/if}
