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
