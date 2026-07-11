import type {
	Action,
	Instance,
	Me,
	RawEvent,
	RequestDetail,
	RequestRollup,
	ServiceHealth,
	SessionsResponse,
	Stage,
	Stats
} from './types';

async function get<T>(path: string): Promise<T> {
	const res = await fetch(path, { headers: { Accept: 'application/json' } });
	if (res.status === 401) {
		// Session expired mid-use — bounce through the SSO login flow.
		window.location.href = '/auth/login?rd=' + encodeURIComponent(window.location.pathname);
		throw new Error('unauthenticated');
	}
	if (!res.ok) throw new Error(`${path}: ${res.status}`);
	return (await res.json()) as T;
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

async function post(path: string, body?: unknown): Promise<void> {
	const res = await fetch(path, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: body ? JSON.stringify(body) : undefined
	});
	if (res.status === 401) {
		window.location.href = '/auth/login?rd=' + encodeURIComponent(window.location.pathname);
		throw new Error('unauthenticated');
	}
	if (!res.ok) throw new Error(`${path}: ${res.status}`);
}

export const retryItem = (mediaItemID: number) => post('/api/actions/retry', { media_item_id: mediaItemID });
export const cancelRequest = (requestID: number) => post('/api/actions/cancel', { request_id: requestID });
export const jellyfinScan = () => post('/api/actions/jellyfin-scan');

export async function getActions(scope = 'global', targetId?: number): Promise<Action[]> {
	const params = new URLSearchParams({ scope });
	if (targetId) params.set('target_id', String(targetId));
	const body = await get<{ actions: Action[] }>(`/api/actions?${params}`);
	return body.actions ?? [];
}

export const executeAction = (kind: string, params: Record<string, unknown>) =>
	post('/api/actions/execute', { kind, params });

export async function getFlowSettings(): Promise<Record<string, string>> {
	const body = await get<{ settings: Record<string, string> }>('/api/flow');
	return body.settings ?? {};
}

export async function putFlowSettings(settings: Record<string, string>): Promise<Record<string, string>> {
	const res = await fetch('/api/flow', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ settings })
	});
	if (res.status === 401) {
		window.location.href = '/auth/login?rd=' + encodeURIComponent(window.location.pathname);
		throw new Error('unauthenticated');
	}
	if (!res.ok) throw new Error(`/api/flow: ${res.status}`);
	const body = (await res.json()) as { settings?: Record<string, string> };
	return body.settings ?? {};
}
