import { api } from '#lib/server/clients.js';
import { errorFrom, failFrom, text } from '#lib/server/rpc.js';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ locals, params }) => {
	try {
		const { payment } = await api(locals).payments.getPayment({ id: params.id });
		if (!payment) throw new Error('payment missing from response');
		return { payment };
	} catch (e) {
		errorFrom(e);
	}
};

export const actions: Actions = {
	refund: async ({ request, locals, params }) => {
		const reason = text(await request.formData(), 'reason').slice(0, 500);
		try {
			await api(locals).payments.refundPayment({ id: params.id, reason });
			return { on: 'refund', refunded: true };
		} catch (e) {
			return failFrom(e, 'refund');
		}
	}
};
