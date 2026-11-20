<script lang="ts">
	import { confirm } from '#lib/confirm.svelte.ts';
	import { cn } from '#lib/utils.ts';

	// Native <dialog> with showModal(): the browser traps focus, makes the page
	// behind inert, closes on Esc and puts focus back on the trigger afterwards.
	let dialog = $state<HTMLDialogElement>();
	let cancelBtn = $state<HTMLButtonElement>();
	let confirmBtn = $state<HTMLButtonElement>();

	$effect(() => {
		if (!dialog) return;
		if (confirm.open && !dialog.open) {
			dialog.showModal();
			// Destructive prompts start on Cancel, so a stray Enter does nothing.
			(confirm.danger ? cancelBtn : confirmBtn)?.focus();
		} else if (!confirm.open && dialog.open) {
			dialog.close();
		}
	});

	// Esc or a click on the backdrop (the dialog element itself, outside the
	// panel) counts as Cancel.
	function onClose() {
		if (confirm.open) confirm.resolve(false);
	}
	function onBackdrop(e: MouseEvent) {
		if (e.target === dialog) confirm.resolve(false);
	}
</script>

<dialog
	bind:this={dialog}
	onclose={onClose}
	onclick={onBackdrop}
	aria-labelledby="confirm-title"
	aria-describedby="confirm-message"
	class="m-auto w-[calc(100%-2rem)] max-w-md rounded-lg border border-border bg-card p-0 text-foreground shadow-xl backdrop:bg-black/60"
>
	<div class="max-h-[80vh] overflow-y-auto p-5">
		<h2 id="confirm-title" class="text-base font-semibold sm:text-sm">{confirm.title}</h2>
		<p id="confirm-message" class="mt-2 text-sm text-muted-foreground sm:text-xs">{confirm.message}</p>
		{#if confirm.body}
			<div class="mt-3">{@render confirm.body()}</div>
		{/if}
		{#if confirm.details.length}
			<ul class="mt-3 space-y-1 text-xs">
				{#each confirm.details as d, i (i)}
					<li class="flex gap-1.5"><span class="text-muted-foreground">•</span><span>{d}</span></li>
				{/each}
			</ul>
		{/if}
		<div class="mt-5 flex justify-end gap-2">
			<button
				bind:this={cancelBtn}
				onclick={() => confirm.resolve(false)}
				class="tap rounded-md px-3 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-foreground sm:text-xs"
			>
				Cancel
			</button>
			<button
				bind:this={confirmBtn}
				onclick={() => confirm.resolve(true)}
				class={cn(
					'tap rounded-md px-3 py-1.5 text-sm font-medium sm:text-xs',
					confirm.danger
						? 'bg-destructive text-white hover:opacity-90'
						: 'bg-primary text-primary-foreground hover:opacity-90'
				)}
			>
				{confirm.confirmLabel}
			</button>
		</div>
	</div>
</dialog>
