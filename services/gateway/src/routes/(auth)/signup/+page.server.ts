import { fail } from '@sveltejs/kit';
import { clients } from '#lib/server/clients.js';
import { failFrom, text } from '#lib/server/rpc.js';
import type { Actions } from './$types';

export const actions: Actions = {
	default: async ({ request }) => {
		const data = await request.formData();
		const fields = {
			name: text(data, 'name'),
			username: text(data, 'username').toLowerCase(),
			email: text(data, 'email')
		};
		const password = String(data.get('password') ?? '');
		if (password.length < 12)
			return fail(400, { ...fields, message: 'Use a password of at least 12 characters.' });

		try {
			const res = await clients().users.createUser({ ...fields, password });
			return { created: res.user?.email ?? fields.email };
		} catch (e) {
			const f = failFrom(e);
			return fail(f.status, { ...fields, ...f.data });
		}
	}
};
