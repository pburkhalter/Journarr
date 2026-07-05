import { getMe, getServices } from './api';
import type { Me, ServiceHealth } from './types';

/**
 * Live state fed by the SSE stream. Components read the runes directly;
 * EventSource reconnects automatically, and a reconnect triggers a refetch
 * so dropped events heal themselves.
 */
class LiveStore {
	services = $state<Record<string, ServiceHealth>>({});
	connected = $state(false);
	me = $state<Me | null>(null);

	private es: EventSource | undefined;

	start() {
		if (this.es) return;
		void this.loadMe();
		void this.refresh();
		this.es = new EventSource('/api/events/stream');
		this.es.onopen = () => {
			this.connected = true;
			void this.refresh();
		};
		this.es.onerror = () => {
			this.connected = false;
		};
		this.es.addEventListener('service.health', (e) => {
			try {
				const h = JSON.parse((e as MessageEvent).data) as ServiceHealth;
				this.services[h.service] = h;
			} catch {
				// malformed frame — next poll pass refreshes the row anyway
			}
		});
	}

	async loadMe() {
		try {
			this.me = await getMe();
		} catch {
			// 401 already triggered the login redirect in api.ts
		}
	}

	async refresh() {
		try {
			const list = await getServices();
			const next: Record<string, ServiceHealth> = {};
			for (const h of list) next[h.service] = h;
			this.services = next;
		} catch {
			// backend unreachable; SSE onerror already flags it
		}
	}
}

export const live = new LiveStore();
