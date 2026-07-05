import type { Me, ServiceHealth, Stats } from './types';

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
