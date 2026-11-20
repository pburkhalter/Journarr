import type { Snippet } from 'svelte';

export interface ConfirmOptions {
	title: string;
	message: string;
	details?: string[];
	/** rich content under the message, e.g. a removal plan */
	body?: Snippet;
	confirmLabel?: string;
	danger?: boolean;
}

// A single app-wide confirm dialog. Any component calls
// `await confirm.ask({...})` and a lone <ConfirmDialog/> in the layout renders
// it — no per-component modal state.
class ConfirmStore {
	open = $state(false);
	title = $state('');
	message = $state('');
	details = $state<string[]>([]);
	body = $state<Snippet | undefined>(undefined);
	confirmLabel = $state('Confirm');
	danger = $state(false);

	private resolver: ((v: boolean) => void) | null = null;

	ask(o: ConfirmOptions): Promise<boolean> {
		// A second ask while one is open answers the first with "no" instead
		// of leaving its caller waiting forever.
		this.settle(false);
		this.title = o.title;
		this.message = o.message;
		this.details = o.details ?? [];
		this.body = o.body;
		this.confirmLabel = o.confirmLabel ?? 'Confirm';
		this.danger = !!o.danger;
		this.open = true;
		return new Promise((res) => (this.resolver = res));
	}

	resolve(v: boolean) {
		this.open = false;
		this.settle(v);
	}

	private settle(v: boolean) {
		const r = this.resolver;
		this.resolver = null;
		r?.(v);
	}
}

export const confirm = new ConfirmStore();
