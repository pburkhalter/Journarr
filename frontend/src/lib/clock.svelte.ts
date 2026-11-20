/**
 * A shared one-second clock. Components that show "X ago" or decide whether
 * data is stale read `clock.now`, so the text keeps moving even when no new
 * data arrives. One interval for the whole app, started on first read.
 */
class Clock {
	#now = $state(Date.now());
	#timer: ReturnType<typeof setInterval> | undefined;

	get now(): number {
		if (!this.#timer && typeof window !== 'undefined') {
			this.#timer = setInterval(() => (this.#now = Date.now()), 1000);
		}
		return this.#now;
	}
}

export const clock = new Clock();
