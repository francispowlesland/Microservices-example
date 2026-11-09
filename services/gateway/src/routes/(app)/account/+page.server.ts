import { fail, redirect } from '@sveltejs/kit';
import { api } from '#lib/server/clients.js';
import { updateProfile } from '#lib/server/profile.js';
import { errorFrom, failFrom } from '#lib/server/rpc.js';
import { clearTokens } from '#lib/server/session.js';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ locals }) => {
	try {
		const { user } = await api(locals).users.getUser({ id: locals.session!.userId });
		if (!user) throw new Error('user missing from response');
		return { user };
	} catch (e) {
		errorFrom(e);
	}
};

export const actions: Actions = {
	profile: async ({ request, locals }) =>
		updateProfile(locals, locals.session!.userId, await request.formData()),

	password: async ({ request, locals, cookies }) => {
		const data = await request.formData();
		const currentPassword = String(data.get('current') ?? '');
		const newPassword = String(data.get('new') ?? '');
		if (newPassword.length < 12)
			return fail(400, {
				on: 'password',
				message: 'Use a new password of at least 12 characters.'
			});
		try {
			await api(locals).auth.changePassword({
				userId: locals.session!.userId,
				currentPassword,
				newPassword
			});
		} catch (e) {
			return failFrom(e, 'password');
		}
		// The service revokes every refresh token on a password change, including ours.
		clearTokens(cookies);
		redirect(303, '/login?notice=password');
	},

	delete: async ({ locals, cookies }) => {
		try {
			await api(locals).users.deleteUser({ id: locals.session!.userId });
		} catch (e) {
			return failFrom(e, 'delete');
		}
		clearTokens(cookies);
		redirect(303, '/login');
	}
};
