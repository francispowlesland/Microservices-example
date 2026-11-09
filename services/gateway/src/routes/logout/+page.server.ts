import { redirect } from '@sveltejs/kit';
import { clients } from '#lib/server/clients.js';
import { clearTokens, refreshTokenOf } from '#lib/server/session.js';
import type { Actions } from './$types';

export const actions: Actions = {
	default: async ({ cookies }) => {
		const refreshToken = refreshTokenOf(cookies);
		// Logout is idempotent on the users service; still sign out locally if it's unreachable.
		if (refreshToken)
			await clients()
				.auth.logout({ refreshToken })
				.catch(() => {});
		clearTokens(cookies);
		redirect(303, '/login');
	}
};
