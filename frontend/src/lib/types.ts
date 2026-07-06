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
	stuck?: number;
}

// Pipeline stages in ordinal order. 'transcode' ships inactive (Tdarr later).
export const STAGES = [
	{ key: 'requested', label: 'Requested' },
	{ key: 'approved', label: 'Approved' },
	{ key: 'grabbed', label: 'Grabbed' },
	{ key: 'submitted', label: 'TorBox' },
	{ key: 'cloud_downloading', label: 'Cloud DL' },
	{ key: 'pulling', label: 'Pulling' },
	{ key: 'downloaded', label: 'Downloaded' },
	{ key: 'imported', label: 'Imported' },
	{ key: 'available', label: 'In Jellyfin' },
	{ key: 'notified', label: 'Notified' }
] as const;

export type StageKey = (typeof STAGES)[number]['key'];

export function stageIndex(key: string): number {
	return STAGES.findIndex((s) => s.key === key);
}

export interface RequestRollup {
	id: number;
	seerr_request_id?: number;
	media_type: 'movie' | 'tv';
	tmdb_id?: number;
	tvdb_id?: number;
	title: string;
	year?: number;
	poster_url?: string;
	requested_by?: string;
	seasons?: string;
	status: 'active' | 'completed' | 'partial' | 'failed' | 'canceled';
	created_at: string;
	updated_at: string;
	item_count: number;
	stage_counts: Record<string, number>;
	last_error?: string;
	stuck_count: number;
}

export interface MediaItem {
	id: number;
	request_id?: number;
	media_type: 'movie' | 'episode';
	season_number?: number;
	episode_number?: number;
	title: string;
	current_stage: string;
	current_cycle: number;
	last_error?: string;
	imported_path?: string;
	stuck_since?: string;
	updated_at: string;
}

export interface Transition {
	id: number;
	media_item_id: number;
	cycle: number;
	stage: string;
	entered_at: string;
	note?: string;
}

export interface ItemDetail extends MediaItem {
	transitions: Transition[];
}

export interface Download {
	id: number;
	client_download_id: string;
	arr: 'sonarr' | 'radarr';
	source?: string;
	release_title?: string;
	indexer?: string;
	size_bytes?: number;
	state: string;
	bytes_downloaded?: number;
	bytes_total?: number;
	last_error?: string;
	grabbed_at?: string;
	updated_at: string;
}

export interface RequestDetail {
	request: RequestRollup;
	items: ItemDetail[];
	downloads: Download[];
}

export interface RawEvent {
	id: number;
	source: string;
	kind: string;
	payload: unknown;
	received_at: string;
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
