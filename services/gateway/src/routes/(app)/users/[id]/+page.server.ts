import { redirect } from '@sveltejs/kit';
import { api } from '#lib/server/clients.js';
import { updateProfile } from '#lib/server/profile.js';
import { errorFrom, failFrom } from '#lib/server/rpc.js';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ locals, params }) => {
	if (params.id === locals.session!.userId) redirect(303, '/account');
	const c = api(locals);
	try {
		const [{ user }, { orders }] = await Promise.all([
			c.users.getUser({ id: params.id }),
			c.orders.listOrders({ userId: params.id, pageSize: 10 })
		]);
		if (!user) throw new Error('user missing from response');
		return { user, orders };
	} catch (e) {
		errorFrom(e);
	}
};

export const actions: Actions = {
	profile: async ({ request, locals, params }) =>
		updateProfile(locals, params.id, await request.formData()),

	delete: async ({ locals, params }) => {
		try {
			await api(locals).users.deleteUser({ id: params.id });
		} catch (e) {
			return failFrom(e, 'delete');
		}
		redirect(303, '/users');
	}
};
