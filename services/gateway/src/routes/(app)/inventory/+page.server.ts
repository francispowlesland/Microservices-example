import { fail } from '@sveltejs/kit';
import { api } from '#lib/server/clients.js';
import { errorFrom, failFrom, pageToken, text } from '#lib/server/rpc.js';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ locals, url }) => {
	try {
		const res = await api(locals).inventory.listInventoryItems({
			pageSize: 50,
			pageToken: pageToken(url)
		});
		return { items: res.items, next: res.nextPageToken };
	} catch (e) {
		errorFrom(e);
	}
};

export const actions: Actions = {
	create: async ({ request, locals }) => {
		const data = await request.formData();
		const sku = text(data, 'sku');
		const quantity = Number(text(data, 'quantity'));
		if (!Number.isInteger(quantity) || quantity < 0 || quantity > 2 ** 31 - 1)
			return fail(400, {
				on: 'create',
				sku,
				message: 'Quantity must be a whole number, 0 or more.'
			});
		try {
			await api(locals).inventory.createInventoryItem({ item: { sku, quantity } });
			return { on: 'create', created: sku.toUpperCase() };
		} catch (e) {
			const f = failFrom(e, 'create');
			return fail(f.status, { ...f.data, sku });
		}
	}
};
