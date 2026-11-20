import type {
	Approval,
	ApprovalDetail,
	Action,
	DiskMount,
	Instance,
	Me,
	RawEvent,
	RecheckReport,
	RemovalPlan,
	RemovalStep,
	RequestDetail,
	RequestRollup,
	ServiceHealth,
	SessionsResponse,
	Stage,
	Stats
} from './types';

/** An HTTP error from the Journarr API; `status` is the response code. */
export class ApiError extends Error {
	constructor(
		readonly path: string,
		readonly status: number
	) {
		super(`${path}: ${status}`);
		this.name = 'ApiError';
	}
}

/** True for a 404 from the API (e.g. a request that no longer exists). */
export function isNotFound(e: unknown): boolean {
	return e instanceof ApiError && e.status === 404;
}

/**
 * Sends the browser through the SSO login and back to where it was,
 * query string included. Called on any 401 (session expired mid-use).
 */
export function toLogin(): never {
	const rd = window.location.pathname + window.location.search;
	window.location.href = '/auth/login?rd=' + encodeURIComponent(rd);
	throw new Error('unauthenticated');
}

/**
 * fetch with the app-wide rules: a 401 goes to the login, and every write
 * carries a JSON body with its Content-Type (the backend refuses
 * state-changing calls without it, as CSRF protection).
 */
async function request(path: string, method = 'GET', body?: unknown): Promise<Response> {
	const init: RequestInit = { method, headers: { Accept: 'application/json' } };
	if (method !== 'GET') {
		init.headers = { Accept: 'application/json', 'Content-Type': 'application/json' };
		init.body = JSON.stringify(body ?? {});
	}
	const res = await fetch(path, init);
	if (res.status === 401) toLogin();
	return res;
}

async function get<T>(path: string): Promise<T> {
	const res = await request(path);
	if (!res.ok) throw new ApiError(path, res.status);
	return (await res.json()) as T;
}

async function post(path: string, body?: unknown): Promise<void> {
	const res = await request(path, 'POST', body);
	if (!res.ok) throw new ApiError(path, res.status);
}

export async function getMe(): Promise<Me> {
	return get<Me>('/api/me');
}

export async function getServices(): Promise<ServiceHealth[]> {
	const body = await get<{ services: ServiceHealth[] }>('/api/services');
	return body.services ?? [];
}

export async function getInstances(): Promise<Instance[]> {
	const body = await get<{ instances: Instance[] }>('/api/instances');
	return body.instances ?? [];
}

export async function getDiskSpace(): Promise<DiskMount[]> {
	const body = await get<{ mounts: DiskMount[] }>('/api/diskspace');
	return body.mounts ?? [];
}

export async function getSessions(): Promise<SessionsResponse> {
	return get<SessionsResponse>('/api/sessions');
}

export async function getStages(): Promise<Stage[]> {
	const body = await get<{ stages: Stage[] }>('/api/stages');
	return body.stages ?? [];
}

export async function getStats(): Promise<Stats> {
	return get<Stats>('/api/stats');
}

export async function getRequests(status = 'active', q = ''): Promise<RequestRollup[]> {
	const params = new URLSearchParams({ status, limit: '200' });
	if (q) params.set('q', q);
	const body = await get<{ requests: RequestRollup[] }>(`/api/requests?${params}`);
	return body.requests ?? [];
}

export async function getRequestDetail(id: number): Promise<RequestDetail> {
	return get<RequestDetail>(`/api/requests/${id}`);
}

export async function getMediaEvents(id: number): Promise<RawEvent[]> {
	const body = await get<{ events: RawEvent[] }>(`/api/media/${id}/events`);
	return body.events ?? [];
}

export const retryItem = (mediaItemID: number) => post('/api/actions/retry', { media_item_id: mediaItemID });
export const cancelRequest = (requestID: number) => post('/api/actions/cancel', { request_id: requestID });

export async function getRemovalPlan(requestID: number): Promise<RemovalPlan> {
	return get<RemovalPlan>(`/api/requests/${requestID}/removal`);
}

/** Why a removal answered 409: someone started watching, or the plan changed. */
export type RemovalConflict = 'watching' | 'plan_changed';

export interface RemovalResult {
	ok: boolean;
	conflict?: RemovalConflict;
	error?: string;
	steps: RemovalStep[];
}

// removeTitle deletes the request's series/movie everywhere. `planHash` comes
// from the preview the user confirmed; the server answers 409 when the plan
// changed since (code plan_changed) or someone is watching (code watching,
// `force` overrides that one).
export async function removeTitle(requestID: number, force = false, planHash?: string): Promise<RemovalResult> {
	const res = await request('/api/actions/remove', 'POST', {
		request_id: requestID,
		force,
		...(planHash ? { plan_hash: planHash } : {})
	});
	const body = (await res.json().catch(() => ({}))) as { error?: string; code?: string; steps?: RemovalStep[] };
	let conflict: RemovalConflict | undefined;
	if (res.status === 409) {
		// Older backends send no code; their only 409 was "someone is watching".
		conflict = body.code === 'plan_changed' ? 'plan_changed' : 'watching';
	}
	return { ok: res.ok, conflict, error: body.error, steps: body.steps ?? [] };
}

export async function getActions(scope = 'global', targetId?: number): Promise<Action[]> {
	const params = new URLSearchParams({ scope });
	if (targetId) params.set('target_id', String(targetId));
	const body = await get<{ actions: Action[] }>(`/api/actions?${params}`);
	return body.actions ?? [];
}

/** Runs a catalog action; resolves to its result (only some actions report one). */
export async function executeAction(kind: string, params: Record<string, unknown>): Promise<unknown> {
	const res = await request('/api/actions/execute', 'POST', { kind, params });
	if (!res.ok) throw new ApiError('/api/actions/execute', res.status);
	const body = (await res.json().catch(() => ({}))) as { result?: unknown };
	return body.result;
}

export const recheckOpen = async () => (await executeAction('recheck-open', {})) as RecheckReport;

export async function getFlowSettings(): Promise<Record<string, string>> {
	const body = await get<{ settings: Record<string, string> }>('/api/flow');
	return body.settings ?? {};
}

export async function putFlowSettings(settings: Record<string, string>): Promise<Record<string, string>> {
	const res = await request('/api/flow', 'PUT', { settings });
	if (!res.ok) throw new ApiError('/api/flow', res.status);
	const body = (await res.json()) as { settings?: Record<string, string> };
	return body.settings ?? {};
}

// ---- removal requests from MCP clients (approved only with an SSO session)

export async function getApprovals(onlyPending = false): Promise<{ approvals: Approval[]; enabled: boolean; sso?: boolean }> {
	return get(`/api/approvals${onlyPending ? '?status=pending' : ''}`);
}

export async function getApproval(id: number): Promise<ApprovalDetail> {
	return get<ApprovalDetail>(`/api/approvals/${id}`);
}

export type ApprovalConflict = 'watching' | 'plan_changed' | 'not_pending' | 'failed';

export interface DecisionResult {
	ok: boolean;
	conflict?: ApprovalConflict;
	error?: string;
	approval?: Approval;
}

/** Approve (runs the removal) or reject a request. 409 carries a code. */
export async function decideApproval(id: number, decision: 'approve' | 'reject', force = false): Promise<DecisionResult> {
	const res = await request(`/api/approvals/${id}/${decision}`, 'POST', decision === 'approve' ? { force } : {});
	const body = (await res.json().catch(() => ({}))) as { error?: string; code?: ApprovalConflict; approval?: Approval };
	return {
		ok: res.ok,
		conflict: res.status === 409 ? (body.code ?? 'failed') : undefined,
		error: res.ok ? undefined : (body.error ?? `HTTP ${res.status}`),
		approval: body.approval
	};
}
