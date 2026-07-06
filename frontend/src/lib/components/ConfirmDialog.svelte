<script lang="ts">
	import { confirm } from '$lib/confirm.svelte';
	import { cn } from '$lib/utils';
</script>

<svelte:window onkeydown={(e) => confirm.open && e.key === 'Escape' && confirm.resolve(false)} />

{#if confirm.open}
	<div class="fixed inset-0 z-50 flex items-center justify-center p-4">
		<button
			class="absolute inset-0 bg-black/60"
			aria-label="Dismiss"
			onclick={() => confirm.resolve(false)}
		></button>
		<div class="relative w-full max-w-sm rounded-lg border border-border bg-card p-5 shadow-xl" role="dialog" aria-modal="true">
			<h2 class="text-sm font-semibold">{confirm.title}</h2>
			<p class="mt-2 text-xs text-muted-foreground">{confirm.message}</p>
			<div class="mt-5 flex justify-end gap-2">
				<button
					onclick={() => confirm.resolve(false)}
					class="rounded-md px-3 py-1.5 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
				>
					Cancel
				</button>
				<button
					onclick={() => confirm.resolve(true)}
					class={cn(
						'rounded-md px-3 py-1.5 text-xs font-medium',
						confirm.danger
							? 'bg-destructive text-white hover:opacity-90'
							: 'bg-primary text-primary-foreground hover:opacity-90'
					)}
				>
					{confirm.confirmLabel}
				</button>
			</div>
		</div>
	</div>
{/if}
