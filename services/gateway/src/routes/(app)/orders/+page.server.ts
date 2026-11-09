import { fail } from '@sveltejs/kit';
import { api } from '#lib/server/clients.js';
import { errorFrom, failFrom, pageToken, text } from '#lib/server/rpc.js';
import { parseCents } from '#lib/format.js';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ locals, url }) => {
	const userId = locals.session!.isAdmin ? (url.searchParams.get('user') ?? '') : '';
	try {
		const res = await api(locals).orders.listOrders({
			pageSize: 25,
			pageToken: pageToken(url),
			userId
		});
		return { orders: res.orders, next: res.nextPageToken, filter: userId };
	} catch (e) {
		errorFrom(e);
	}
};

export const actions: Actions = {
	create: async ({ request, locals }) => {
		const data = await request.formData();
		const totalCents = parseCents(text(data, 'total'));
		if (totalCents === null)
			return fail(400, { on: 'create', message: 'Enter a total such as 24.99.' });
		const userId = locals.session!.isAdmin ? text(data, 'userId') : '';
		try {
			const res = await api(locals).orders.createOrder({ order: { userId, totalCents } });
			return { on: 'create', created: res.order?.id };
		} catch (e) {
			return failFrom(e, 'create');
		}
	}
};
