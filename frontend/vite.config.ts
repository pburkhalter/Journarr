import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import { createHash } from 'node:crypto';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { defineConfig } from 'vite';

// The app version SvelteKit bakes into the build. Its default is the build
// time, which changes every chunk hash on every build; a hash of the inputs
// makes the build reproducible, so CI can check that the committed build
// matches the source. It still changes whenever the UI changes.
function sourceHash(paths: string[]): string {
	const hash = createHash('sha256');
	const walk = (p: string) => {
		if (statSync(p).isDirectory()) {
			for (const name of readdirSync(p).sort()) walk(join(p, name));
		} else {
			hash.update(p).update('\0').update(readFileSync(p));
		}
	};
	for (const p of paths) walk(p);
	return hash.digest('hex').slice(0, 12);
}

export default defineConfig({
	plugins: [
		tailwindcss(),
		// SvelteKit 3 takes its configuration here (svelte.config.js is gone).
		sveltekit({
			preprocess: vitePreprocess(),
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
				name: sourceHash(['src', 'static', 'package-lock.json', 'vite.config.ts'])
			}
		})
	],
	server: {
		// Dev mode: Go backend runs on :8484, Vite serves the frontend.
		proxy: {
			'/api': 'http://127.0.0.1:8484',
			'/healthz': 'http://127.0.0.1:8484'
		}
	}
});
