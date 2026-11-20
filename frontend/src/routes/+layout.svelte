<script lang="ts">
	import '../app.css';
	import { page } from '$app/state';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import { live } from '$lib/live.svelte';
	import { cn } from '$lib/utils';
	import { onMount } from 'svelte';

	let { children } = $props();

	onMount(() => live.start());

	// History is the Flow page with the "Done" filter, so both entries point at
	// "/" and the status parameter decides which one is highlighted.
	const nav: { href: string; label: string; active: (u: URL) => boolean }[] = [
		{
			href: '/',
			label: 'Flow',
			active: (u) =>
				(u.pathname === '/' && u.searchParams.get('status') !== 'done') || u.pathname.startsWith('/requests/')
		},
		{
			href: '/?status=done',
			label: 'History',
			active: (u) => u.pathname === '/' && u.searchParams.get('status') === 'done'
		},
		{ href: '/actions', label: 'Actions', active: (u) => u.pathname === '/actions' },
		{ href: '/services', label: 'Services', active: (u) => u.pathname === '/services' }
	];
</script>

<!--
	Responsive shell: on mobile the sidebar becomes a compact top bar
	(logo + horizontal nav + live dot); md+ is the original left sidebar,
	unchanged.
-->
<div class="flex min-h-screen flex-col md:flex-row">
	<aside
		class="flex shrink-0 flex-col border-b border-border bg-card/40 md:w-56 md:border-b-0 md:border-r"
	>
		<div class="flex items-center justify-between gap-3 px-4 py-3 md:block md:p-0">
			<a href="/" class="flex items-center gap-2.5 md:px-5 md:py-5">
				<img src="/favicon.svg" alt="" class="size-7 rounded-md" />
				<div>
					<div class="text-sm font-semibold tracking-tight">Journarr</div>
					<div class="hidden text-[11px] text-muted-foreground md:block">request flow tracker</div>
				</div>
			</a>
			<!-- mobile-only live indicator (desktop shows it in the footer) -->
			<span class="flex items-center gap-1.5 text-[11px] text-muted-foreground md:hidden" role="status">
				<span
					class={cn('size-2 shrink-0 rounded-full', live.connected ? 'bg-success' : 'bg-destructive')}
					aria-hidden="true"
				></span>
				{live.connected ? 'live' : 'offline'}
			</span>
		</div>
		<nav class="flex gap-1 overflow-x-auto px-3 pb-2 md:flex-col md:py-2">
			{#each nav as item (item.href)}
				{@const active = item.active(page.url)}
				<a
					href={item.href}
					aria-current={active ? 'page' : undefined}
					class={cn(
						'tap flex items-center rounded-md px-3 py-2 text-sm transition-colors',
						active
							? 'bg-accent text-accent-foreground font-medium'
							: 'text-muted-foreground hover:bg-accent/50 hover:text-foreground'
					)}
				>
					{item.label}
				</a>
			{/each}
		</nav>
		<div class="mt-auto hidden px-5 py-4 md:block">
			{#if live.me?.auth_enabled && live.me.user}
				<div class="mb-3 flex items-center gap-2.5 border-t border-border pt-3">
					{#if live.me.user.picture}
						<img src={live.me.user.picture} alt="" class="size-7 rounded-full" />
					{:else}
						<span
							class="flex size-7 items-center justify-center rounded-full bg-accent text-[11px] font-medium"
						>
							{(live.me.user.name ?? live.me.user.email ?? '?').charAt(0).toUpperCase()}
						</span>
					{/if}
					<div class="min-w-0 flex-1">
						<div class="truncate text-xs font-medium">
							{live.me.user.name ?? live.me.user.email}
						</div>
						<a href="/auth/logout" data-sveltekit-reload class="text-[11px] text-muted-foreground hover:text-foreground">
							Sign out
						</a>
					</div>
				</div>
			{/if}
			<div class="flex items-center gap-2 text-[11px] text-muted-foreground" role="status">
				<span
					class={cn('size-2 rounded-full', live.connected ? 'bg-success' : 'bg-destructive')}
					aria-hidden="true"
				></span>
				{live.connected ? 'live' : 'reconnecting…'}
			</div>
		</div>
	</aside>

	<main class="min-w-0 flex-1 overflow-x-hidden px-4 py-5 md:px-8 md:py-7">
		{@render children()}
	</main>
</div>

<ConfirmDialog />
