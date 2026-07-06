// A single app-wide confirm dialog. Any component calls
// `await confirm.ask({...})` and a lone <ConfirmDialog/> in the layout renders
// it — no per-component modal state.
class ConfirmStore {
	open = $state(false);
	title = $state('');
	message = $state('');
	confirmLabel = $state('Confirm');
	danger = $state(false);
	busy = $state(false);

	private resolver: ((v: boolean) => void) | null = null;

	ask(o: { title: string; message: string; confirmLabel?: string; danger?: boolean }): Promise<boolean> {
		this.title = o.title;
		this.message = o.message;
		this.confirmLabel = o.confirmLabel ?? 'Confirm';
		this.danger = !!o.danger;
		this.busy = false;
		this.open = true;
		return new Promise((res) => (this.resolver = res));
	}

	resolve(v: boolean) {
		this.open = false;
		const r = this.resolver;
		this.resolver = null;
		r?.(v);
	}
}

export const confirm = new ConfirmStore();
