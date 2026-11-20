import { getInstances, getMe, getServices, getStages } from './api';
import { STAGES, type Instance, type Me, type ServiceHealth, type Stage } from './types';

/** Longest wait between reconnect attempts. */
const MAX_BACKOFF_MS = 30_000;

/**
 * Live state fed by the SSE stream. Components read the runes directly.
 *
 * The browser's own EventSource retry gives up for good after an HTTP error
 * (a 502 while the proxy waits for a redeploy, a 401 once the session
 * expired), so the store reconnects itself with backoff, checks the login on
 * the way, and refreshes when the tab comes back to the foreground (iOS
 * suspends background tabs and the stream can look open while it is dead).
 * Every (re)connect refetches, so events missed in between heal themselves.
 */
class LiveStore {
	services = $state<Record<string, ServiceHealth>>({});
	/** true once /api/services answered at least once */
	servicesLoaded = $state(false);
	/** true while /api/services cannot be reached */
	servicesError = $state(false);
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

	/** The stage skeleton every component draws: live catalog, else the fallback. */
	get stageList(): { key: string; label: string }[] {
		return this.stages.length
			? this.stages.map((s) => ({ key: s.key, label: s.label }))
			: STAGES.map((s) => ({ key: s.key, label: s.label }));
	}

	/** Position of a stage in stageList, -1 if unknown. */
	stageIndex(key: string): number {
		return this.stageList.findIndex((s) => s.key === key);
	}

	stageLabel(key: string): string {
		return this.stageList.find((s) => s.key === key)?.label ?? key;
	}

	private es: EventSource | undefined;
	private tickTimer: ReturnType<typeof setTimeout> | undefined;
	private retryTimer: ReturnType<typeof setTimeout> | undefined;
	private attempt = 0;
	private started = false;

	start() {
		if (this.started) return;
		this.started = true;
		void this.loadMe();
		void this.refresh();
		this.connect();
		document.addEventListener('visibilitychange', () => {
			if (document.visibilityState !== 'visible') return;
			void this.refresh();
			this.pipelineTick++;
			if (this.es?.readyState !== EventSource.OPEN) {
				this.attempt = 0;
				this.connect();
			}
		});
	}

	private connect() {
		clearTimeout(this.retryTimer);
		this.es?.close();
		const es = new EventSource('/api/events/stream');
		this.es = es;
		es.onopen = () => {
			this.connected = true;
			this.attempt = 0;
			void this.refresh();
			// heal pipeline views too — events during a disconnect are gone
			this.pipelineTick++;
		};
		es.onerror = () => {
			this.connected = false;
			// CONNECTING means the browser retries on its own; CLOSED means it
			// gave up (HTTP error) and only we will bring the stream back.
			if (es.readyState === EventSource.CLOSED) this.scheduleReconnect();
		};
		es.addEventListener('service.health', (e) => {
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
		es.addEventListener('media.stage', bump);
		es.addEventListener('request.updated', bump);
		es.addEventListener('request.removed', bump);
		es.addEventListener('download.progress', (e) => {
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

	private scheduleReconnect() {
		clearTimeout(this.retryTimer);
		const base = Math.min(MAX_BACKOFF_MS, 1000 * 2 ** this.attempt);
		this.attempt++;
		const delay = base / 2 + Math.random() * (base / 2);
		this.retryTimer = setTimeout(async () => {
			// An expired session is the usual reason the stream was refused:
			// /api/me answers 401 then, and api.ts sends the browser to the login.
			await this.loadMe();
			this.connect();
		}, delay);
	}

	async loadMe() {
		try {
			this.me = await getMe();
		} catch {
			// 401 already triggered the login redirect in api.ts; anything else
			// means the backend is unreachable and the reconnect loop goes on
		}
	}

	async refresh() {
		try {
			const list = await getServices();
			const next: Record<string, ServiceHealth> = {};
			for (const h of list) next[h.service] = h;
			this.services = next;
			this.servicesLoaded = true;
			this.servicesError = false;
		} catch {
			this.servicesError = true;
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
