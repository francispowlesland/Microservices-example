import { fail } from '@sveltejs/kit';
import { api } from '#lib/server/clients.js';
import { errorFrom, failFrom, pageToken, text } from '#lib/server/rpc.js';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ locals, url }) => {
	const userId = locals.session!.isAdmin ? (url.searchParams.get('user') ?? '') : '';
	try {
		const res = await api(locals).orders.listOrders({
			pageSize: 25,
			pageToken: pageToken(url),
			userId
		});
		// Choices for the order form; the first 100 products is enough for now.
		const stock = await api(locals).inventory.listInventoryItems({ pageSize: 100 });
		return { orders: res.orders, next: res.nextPageToken, filter: userId, products: stock.items };
	} catch (e) {
		errorFrom(e);
	}
};

export const actions: Actions = {
	create: async ({ request, locals }) => {
		const data = await request.formData();
		const skus = data.getAll('sku').map(String);
		const quantities = data.getAll('quantity').map(Number);
		const lines = skus
			.map((sku, i) => ({ sku, quantity: quantities[i] }))
			.filter((l) => l.sku !== '');
		if (lines.length === 0)
			return fail(400, { on: 'create', message: 'Add at least one product.' });
		if (lines.some((l) => !Number.isInteger(l.quantity) || l.quantity < 1))
			return fail(400, { on: 'create', message: 'Quantities must be whole numbers, 1 or more.' });
		const userId = locals.session!.isAdmin ? text(data, 'userId') : '';
		try {
			const res = await api(locals).orders.createOrder({ order: { userId, lines } });
			return { on: 'create', created: res.order?.id };
		} catch (e) {
			return failFrom(e, 'create');
		}
	}
};
