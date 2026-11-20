export type ServiceStatus = 'up' | 'degraded' | 'down';

export interface ServiceHealth {
	service: string;
	status: ServiceStatus;
	latency_ms: number;
	version?: string;
	/** raw JSON blob with per-service extras — parse with parseDetail() */
	detail?: string;
	checked_at: string;
	/** GitHub release-update status for the self-hosted custom stack */
	update?: { current: string; latest: string; update_available: boolean };
}

export interface TorboxCreate {
	available: number;
	capacity: number;
}

/**
 * Per-service extras in ServiceHealth.detail, as the backend health checks
 * write them (backend/internal/clients/*.go, CheckHealth). Every field is
 * optional: each service kind fills only its own.
 */
export interface ServiceDetail {
	/** any kind: the check failed (clients.down) */
	error?: string;
	/** sonarr/radarr/prowlarr: /system/status answered, /health did not */
	health_error?: string;
	/** sonarr/radarr/prowlarr: health warnings and errors from the arr */
	health_messages?: string[];
	health_warnings?: number;
	/** prowlarr: daily grab quota of the configured indexer */
	grab_quota?: { indexer: string; used: number; cap: number; left: number };
	/** arrarr: job counts per state */
	states?: Record<string, number>;
	/** arrarr: TorBox create headroom */
	torbox_create?: TorboxCreate;
	/** arrarr: downloads in flight (coarse state, no byte progress) */
	downloads?: { title: string; state: string }[];
	download_count?: number;
	/** arrarr: why the TorBox poller counts as stalled */
	poller?: string;
	/** seerr: Seerr asks for a restart (settings changed) */
	restart_required?: boolean;
	/** jellyfin */
	server_name?: string;
	/** waha: WhatsApp sessions */
	sessions?: { name: string; status: string }[];
	/** tdarr */
	nodes?: number;
	gpu_workers?: number;
	transcodes?: { file: string; percentage: number; fps: number; eta: string; type?: string }[];
}

/** Registry instance metadata (GET /api/instances) — drives tile order/label/fold. */
export interface Instance {
	id: string;
	kind: string;
	label: string;
	order: number;
	parent_id?: string;
	capabilities: string[];
	stages?: string[];
}

/** Active pipeline stage (GET /api/stages) — the canonical, single-sourced enum. */
export interface Stage {
	key: string;
	ordinal: number;
	label: string;
	active: boolean;
}

/** A capability-derived action (GET /api/actions), invoked via /api/actions/execute. */
export interface Action {
	id: string;
	label: string;
	kind: string; // recheck-open|library-scan|transcode-*|cancel|resend-notify|season-search
	scope: string; // global|request|season|item
	instance_id?: string;
	request_id?: number;
	season?: number;
	danger?: boolean;
}

/** What one item turned out to be in a re-check of the open requests. */
export type RecheckOutcome =
	| 'error'
	| 'no_file'
	| 'not_in_arr'
	| 'reconciled'
	| 'reset'
	| 'missing'
	| 'unmonitored'
	| 'downloading'
	| 'waiting'
	| 'done';

export interface RecheckRequest {
	id: number;
	title: string;
	media_type: 'movie' | 'tv';
	outcome: RecheckOutcome;
	counts: Partial<Record<RecheckOutcome, number>>;
}

/** Result of "Re-check open requests"; the counts are items. */
export interface RecheckReport {
	open: number;
	/** Shown as available without an import, and Sonarr/Radarr have no file: taken back. */
	no_file: number;
	missing: number;
	reset: number;
	reconciled: number;
	waiting: number;
	downloading: number;
	unmonitored: number;
	not_in_arr: number;
	search_skipped?: boolean;
	searched_at?: string;
	errors?: string[];
	requests: RecheckRequest[];
}

export interface Stats {
	requests: Record<string, number>;
	media_items: Record<string, number>;
	stuck?: number;
}

// Fallback stage list used only until GET /api/stages loads into live.stages
// (the canonical source; read it through live.stageList / live.stageIndex).
// 'transcode' is omitted here; the backend catalog gates it behind a Tdarr
// instance.
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

export interface DiskMount {
	path: string;
	label?: string;
	free_space: number;
	total_space: number;
	used_space: number;
	used_percent: number;
	is_library: boolean;
	reported_by: string[];
}

export interface JellySession {
	user: string;
	client: string;
	device: string;
	title: string;
	media_type: string;
	position_sec: number;
	runtime_sec: number;
	paused: boolean;
	play_method: string; // DirectPlay | DirectStream | Transcode
	remote_ip?: string;
}

export interface SessionsResponse {
	sessions: JellySession[];
	safe_to_restart: boolean;
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
	/** set when the request is a movie not yet released (Radarr's expected date) */
	awaiting_release_at?: string;
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

// What "Remove everywhere" would delete (GET /api/requests/{id}/removal).
export interface RemovalPlan {
	request_id: number;
	title: string;
	media_type: 'movie' | 'tv';
	requests: number;
	arr?: string;
	arr_id?: number;
	path?: string;
	files: number;
	size_bytes: number;
	seasons?: number;
	downloads: string[];
	in_seerr: boolean;
	recycle_bin: string; // '' = files are deleted for good
	recycle_days: number;
	viewers: string[];
	warnings: string[];
	/** hash of what the plan deletes; sent back on remove so the server can
	 * refuse when the plan changed in between (409, code plan_changed) */
	plan_hash?: string;
}

export interface RemovalStep {
	step: string;
	status: 'ok' | 'skipped' | 'warning' | 'failed';
	detail?: string;
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

export function parseDetail(h: ServiceHealth): ServiceDetail {
	if (!h.detail) return {};
	try {
		const v: unknown = JSON.parse(h.detail);
		return v && typeof v === 'object' ? (v as ServiceDetail) : {};
	} catch {
		return {};
	}
}

/** A removal an MCP client asked for; the owner approves or rejects it. */
export interface Approval {
	id: number;
	kind: 'remove';
	title: string;
	target: { media_type?: string; tmdb_id?: number; tvdb_id?: number; request_id?: number };
	plan: RemovalPlan;
	plan_hash: string;
	reason?: string;
	requested_by: string;
	status: 'pending' | 'approved' | 'rejected' | 'expired' | 'executed' | 'failed';
	created_at: string;
	expires_at: string;
	decided_at?: string;
	decided_by?: string;
	result?: { error?: string; steps?: RemovalStep[] };
}

export interface ApprovalDetail {
	approval: Approval;
	/** a fresh plan, only for pending requests */
	current_plan?: RemovalPlan;
	current_plan_error?: string;
	/** true when the title changed since the request — approving would fail */
	plan_changed?: boolean;
}
