import { fail, redirect } from '@sveltejs/kit';
import { clients } from '#lib/server/clients.js';
import { failFrom, text } from '#lib/server/rpc.js';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = ({ url }) => ({ token: url.searchParams.get('token') ?? '' });

export const actions: Actions = {
	default: async ({ request }) => {
		const verificationToken = text(await request.formData(), 'token');
		if (!verificationToken) return fail(400, { message: 'Paste the token from your email.' });
		try {
			await clients().auth.verifyEmail({ verificationToken });
		} catch (e) {
			return failFrom(e);
		}
		redirect(303, '/login?notice=verified');
	}
};
