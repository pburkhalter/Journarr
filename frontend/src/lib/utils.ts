import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
	return twMerge(clsx(inputs));
}

/**
 * "12s ago", "3m ago", … Pass `now` from the shared clock (clock.now) so the
 * text keeps ticking; without it the value is frozen at render time.
 */
export function relativeTime(iso: string, now: number = Date.now()): string {
	const then = new Date(iso).getTime();
	if (Number.isNaN(then)) return '–';
	const s = Math.max(0, Math.round((now - then) / 1000));
	if (s < 5) return 'just now';
	if (s < 60) return `${s}s ago`;
	const m = Math.round(s / 60);
	if (m < 60) return `${m}m ago`;
	const h = Math.round(m / 60);
	if (h < 24) return `${h}h ago`;
	return `${Math.round(h / 24)}d ago`;
}

/** "in 3h" for a time ahead of now, "now" once it has passed. */
export function timeUntil(iso: string, now: number = Date.now()): string {
	const then = new Date(iso).getTime();
	if (Number.isNaN(then)) return '–';
	const s = Math.round((then - now) / 1000);
	if (s <= 0) return 'now';
	if (s < 60) return `in ${s}s`;
	const m = Math.round(s / 60);
	if (m < 60) return `in ${m}m`;
	const h = Math.round(m / 60);
	if (h < 48) return `in ${h}h`;
	return `in ${Math.round(h / 24)}d`;
}

/** Local date and time, or the raw value if it does not parse. */
export function formatTime(iso: string): string {
	const d = new Date(iso);
	return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export function titleCase(s: string): string {
	return s.charAt(0).toUpperCase() + s.slice(1);
}

// Binary units — that is what the arrs report and what ZFS/df show, so the
// numbers match when someone cross-checks on the NAS. This is the one byte
// formatter of the app; every size goes through it.
export function formatBytes(n: number | undefined | null): string {
	if (n == null || !Number.isFinite(n) || n <= 0) return '0 B';
	const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
	let i = 0;
	let v = n;
	while (v >= 1024 && i < units.length - 1) {
		v /= 1024;
		i++;
	}
	// Sub-10 values keep a decimal so "9.4 TiB" does not collapse to "9 TiB".
	return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

/** formatBytes, but "–" when the size is unknown (0 or missing). */
export function formatBytesOrDash(n: number | undefined | null): string {
	return n && n > 0 ? formatBytes(n) : '–';
}
