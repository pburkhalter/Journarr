import type { Me, RawEvent, RequestDetail, RequestRollup, ServiceHealth, Stats } from './types';

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
