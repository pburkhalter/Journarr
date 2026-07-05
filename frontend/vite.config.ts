import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	server: {
		// Dev mode: Go backend runs on :8484, Vite serves the frontend.
		proxy: {
			'/api': 'http://127.0.0.1:8484',
			'/healthz': 'http://127.0.0.1:8484'
		}
	}
});
