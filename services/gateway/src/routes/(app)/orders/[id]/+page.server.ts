import { fail, redirect } from '@sveltejs/kit';
import { api } from '#lib/server/clients.js';
import { errorFrom, failFrom, text } from '#lib/server/rpc.js';
import { parseCents } from '#lib/format.js';
import type { Actions, PageServerLoad } from './$types';

const currencies = ['GBP', 'EUR', 'USD'];

export const load: PageServerLoad = async ({ locals, params }) => {
	const c = api(locals);
	try {
		const [{ order }, { payments }] = await Promise.all([
			c.orders.getOrder({ id: params.id }),
			c.payments.listPayments({ orderId: params.id, pageSize: 50 })
		]);
		if (!order) throw new Error('order missing from response');
		// A fresh key per page view: resubmitting the same form can't charge twice.
		return { order, payments, idempotencyKey: crypto.randomUUID(), currencies };
	} catch (e) {
		errorFrom(e);
	}
};

export const actions: Actions = {
	pay: async ({ request, locals, params }) => {
		const data = await request.formData();
		const amountCents = parseCents(text(data, 'amount'));
		const currency = text(data, 'currency');
		const idempotencyKey = text(data, 'key');
		if (amountCents === null || amountCents <= 0n)
			return fail(400, { on: 'pay', message: 'Enter an amount above zero.' });
		if (!currencies.includes(currency))
			return fail(400, { on: 'pay', message: 'Choose a currency.' });
		try {
			const { payment } = await api(locals).payments.processPayment({
				orderId: params.id,
				amountCents,
				currency,
				idempotencyKey
			});
			return { on: 'pay', paymentId: payment?.id };
		} catch (e) {
			return failFrom(e, 'pay');
		}
	},

	update: async ({ request, locals, params }) => {
		const data = await request.formData();
		const userId = text(data, 'userId');
		try {
			await api(locals).orders.updateOrder({ order: { id: params.id, userId } });
			return { on: 'update', saved: true };
		} catch (e) {
			return failFrom(e, 'update');
		}
	},

	delete: async ({ locals, params }) => {
		try {
			await api(locals).orders.deleteOrder({ id: params.id });
		} catch (e) {
			return failFrom(e, 'delete');
		}
		redirect(303, '/orders');
	}
};
