import { confirm } from './confirm.svelte';

// Confirmations used from more than one page, so the same action always asks
// the same question.

export function confirmJellyfinScan(): Promise<boolean> {
	return confirm.ask({
		title: 'Trigger Jellyfin scan',
		message: 'Start a full library refresh in Jellyfin?',
		confirmLabel: 'Scan'
	});
}
