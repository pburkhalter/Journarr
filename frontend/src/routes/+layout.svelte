<script lang="ts">
	import '../app.css';
	import { page } from '$app/state';
	import { live } from '$lib/live.svelte';
	import { cn } from '$lib/utils';
	import { onMount } from 'svelte';

	let { children } = $props();

	onMount(() => live.start());

	const nav = [
		{ href: '/', label: 'Flow' },
		{ href: '/services', label: 'Services' }
	];
</script>

<div class="flex min-h-screen">
	<aside class="flex w-56 shrink-0 flex-col border-r border-border bg-card/40">
		<a href="/" class="flex items-center gap-2.5 px-5 py-5">
			<img src="/favicon.svg" alt="" class="size-7 rounded-md" />
			<div>
				<div class="text-sm font-semibold tracking-tight">Journarr</div>
				<div class="text-[11px] text-muted-foreground">request flow tracker</div>
			</div>
		</a>
		<nav class="flex flex-col gap-1 px-3 py-2">
			{#each nav as item (item.href)}
				<a
					href={item.href}
					class={cn(
						'rounded-md px-3 py-2 text-sm transition-colors',
						page.url.pathname === item.href
							? 'bg-accent text-accent-foreground font-medium'
							: 'text-muted-foreground hover:bg-accent/50 hover:text-foreground'
					)}
				>
					{item.label}
				</a>
			{/each}
			<span class="cursor-default rounded-md px-3 py-2 text-sm text-muted-foreground/40">
				History <span class="text-[10px]">(soon)</span>
			</span>
		</nav>
		<div class="mt-auto px-5 py-4">
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
			<div class="flex items-center gap-2 text-[11px] text-muted-foreground">
				<span
					class={cn('size-2 rounded-full', live.connected ? 'bg-success' : 'bg-destructive')}
				></span>
				{live.connected ? 'live' : 'reconnecting…'}
			</div>
		</div>
	</aside>

	<main class="min-w-0 flex-1 px-8 py-7">
		{@render children()}
	</main>
</div>
