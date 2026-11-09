import tailwindcss from '@tailwindcss/vite';
import adapter from '@sveltejs/adapter-node';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},

			// adapter-node assumes https without a proxy header, so plain-http form POSTs look cross-site.
			// shortcut: build-time and localhost only, move to a real origin or PROTOCOL_HEADER behind TLS.
			csrf: { trustedOrigins: ['http://localhost:3000'] },

			adapter: adapter()
		})
	]
});
