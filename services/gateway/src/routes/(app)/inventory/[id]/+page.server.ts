import { fail, redirect } from '@sveltejs/kit';
import { api } from '#lib/server/clients.js';
import { errorFrom, failFrom, text } from '#lib/server/rpc.js';
import { parseCents } from '#lib/format.js';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ locals, params }) => {
	try {
		const { item } = await api(locals).inventory.getInventoryItem({ id: params.id });
		if (!item) throw new Error('item missing from response');
		return { item };
	} catch (e) {
		errorFrom(e);
	}
};

export const actions: Actions = {
	update: async ({ request, locals, params }) => {
		const data = await request.formData();
		const sku = text(data, 'sku');
		const quantity = Number(text(data, 'quantity'));
		if (!Number.isInteger(quantity) || quantity < 0 || quantity > 2 ** 31 - 1)
			return fail(400, { on: 'update', message: 'Quantity must be a whole number, 0 or more.' });
		const priceCents = parseCents(text(data, 'price'));
		if (priceCents === null || priceCents > 1_000_000_000n)
			return fail(400, { on: 'update', message: 'Enter a price such as 4.99.' });
		try {
			await api(locals).inventory.updateInventoryItem({
				item: { id: params.id, sku, quantity, priceCents }
			});
			return { on: 'update', saved: true };
		} catch (e) {
			return failFrom(e, 'update');
		}
	},

	delete: async ({ locals, params }) => {
		try {
			await api(locals).inventory.deleteInventoryItem({ id: params.id });
		} catch (e) {
			return failFrom(e, 'delete');
		}
		redirect(303, '/inventory');
	}
};
