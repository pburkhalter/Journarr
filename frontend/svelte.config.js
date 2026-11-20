import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { createHash } from 'node:crypto';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

// The app version SvelteKit bakes into the build. Its default is the build
// time, which changes every chunk hash on every build; a hash of the inputs
// makes the build reproducible, so CI can check that the committed build
// matches the source. It still changes whenever the UI changes.
function sourceHash(paths) {
	const hash = createHash('sha256');
	const walk = (p) => {
		if (statSync(p).isDirectory()) {
			for (const name of readdirSync(p).sort()) walk(join(p, name));
		} else {
			hash.update(p).update('\0').update(readFileSync(p));
		}
	};
	for (const p of paths) walk(p);
	return hash.digest('hex').slice(0, 12);
}

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		// Build straight into the Go embed target. The adapter wipes the
		// directory first; the result is committed, so commit the whole
		// directory after a build (stale hashed files disappear with it).
		adapter: adapter({
			pages: '../backend/internal/web/dist',
			assets: '../backend/internal/web/dist',
			fallback: 'index.html',
			strict: false
		}),
		version: {
			name: sourceHash(['src', 'static', 'package-lock.json', 'svelte.config.js', 'vite.config.ts'])
		}
	}
};

export default config;
