export type ServiceStatus = 'up' | 'degraded' | 'down';

export interface ServiceHealth {
	service: string;
	status: ServiceStatus;
	latency_ms: number;
	version?: string;
	/** raw JSON blob with per-service extras — parse with parseDetail() */
	detail?: string;
	checked_at: string;
}

export interface TorboxCreate {
	available: number;
	capacity: number;
}

export interface Stats {
	requests: Record<string, number>;
	media_items: Record<string, number>;
}

export interface Me {
	auth_enabled: boolean;
	user?: {
		sub: string;
		email?: string;
		name?: string;
		picture?: string;
	};
}

export function parseDetail(h: ServiceHealth): Record<string, unknown> {
	if (!h.detail) return {};
	try {
		return JSON.parse(h.detail) as Record<string, unknown>;
	} catch {
		return {};
	}
}
