import { getInstances, getMe, getServices, getStages } from './api';
import type { Instance, Me, ServiceHealth, Stage } from './types';

/**
 * Live state fed by the SSE stream. Components read the runes directly;
 * EventSource reconnects automatically, and a reconnect triggers a refetch
 * so dropped events heal themselves.
 */
class LiveStore {
	services = $state<Record<string, ServiceHealth>>({});
	/** registry instances (tile order/label/fold); empty ⇒ fall back to hardcoded order */
	instances = $state<Instance[]>([]);
	/** active pipeline stage catalog; empty ⇒ fall back to the STAGES const */
	stages = $state<Stage[]>([]);
	connected = $state(false);
	me = $state<Me | null>(null);
	/** bumped on every pipeline change — pages refetch via $effect */
	pipelineTick = $state(0);
	/** live download progress, keyed by download id */
	progress = $state<Record<number, { bytes_downloaded: number; bytes_total: number }>>({});

	private es: EventSource | undefined;
	private tickTimer: ReturnType<typeof setTimeout> | undefined;

	start() {
		if (this.es) return;
		void this.loadMe();
		void this.refresh();
		this.es = new EventSource('/api/events/stream');
		this.es.onopen = () => {
			this.connected = true;
			void this.refresh();
			// heal pipeline views too — events during a disconnect are gone
			this.pipelineTick++;
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
		const bump = () => {
			// debounce: a season import fires dozens of media.stage events
			clearTimeout(this.tickTimer);
			this.tickTimer = setTimeout(() => this.pipelineTick++, 400);
		};
		this.es.addEventListener('media.stage', bump);
		this.es.addEventListener('request.updated', bump);
		this.es.addEventListener('download.progress', (e) => {
			try {
				const p = JSON.parse((e as MessageEvent).data) as {
					download_id: number;
					bytes_downloaded: number;
					bytes_total: number;
				};
				this.progress[p.download_id] = p;
			} catch {
				// ignore
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
		// Instances + stages change rarely and are best-effort: an older backend
		// without these endpoints leaves them empty and the UI falls back.
		try {
			this.instances = await getInstances();
		} catch {
			// keep fallback (hardcoded order)
		}
		try {
			this.stages = await getStages();
		} catch {
			// keep fallback (STAGES const)
		}
	}
}

export const live = new LiveStore();
