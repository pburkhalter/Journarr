import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		// Build straight into the Go embed target. `make frontend` restores
		// the dist/.gitignore afterwards (the adapter wipes the directory).
		adapter: adapter({
			pages: '../backend/internal/web/dist',
			assets: '../backend/internal/web/dist',
			fallback: 'index.html',
			strict: false
		})
	}
};

export default config;
