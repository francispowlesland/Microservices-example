import { fail, redirect } from '@sveltejs/kit';
import { clients } from '#lib/server/clients.js';
import { failFrom, text } from '#lib/server/rpc.js';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = ({ url }) => ({ token: url.searchParams.get('token') ?? '' });

export const actions: Actions = {
	default: async ({ request }) => {
		const data = await request.formData();
		const resetToken = text(data, 'token');
		const newPassword = String(data.get('password') ?? '');
		if (!resetToken) return fail(400, { message: 'Paste the token from your reset email.' });
		if (newPassword.length < 12)
			return fail(400, { message: 'Use a password of at least 12 characters.' });
		try {
			await clients().auth.resetPassword({ resetToken, newPassword });
		} catch (e) {
			return failFrom(e);
		}
		redirect(303, '/login?notice=reset');
	}
};
