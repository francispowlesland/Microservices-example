import { fail } from '@sveltejs/kit';
import { clients } from '#lib/server/clients.js';
import { failFrom, text } from '#lib/server/rpc.js';
import type { Actions } from './$types';

export const actions: Actions = {
	default: async ({ request }) => {
		const email = text(await request.formData(), 'email');
		if (!email) return fail(400, { message: 'Enter the email you signed up with.' });
		try {
			await clients().auth.requestPasswordReset({ email });
		} catch (e) {
			return failFrom(e);
		}
		return { sent: true };
	}
};
