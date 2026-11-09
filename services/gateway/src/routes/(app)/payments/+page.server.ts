import { api } from '#lib/server/clients.js';
import { errorFrom, pageToken } from '#lib/server/rpc.js';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ locals, url }) => {
	const orderId = url.searchParams.get('order') ?? '';
	try {
		const res = await api(locals).payments.listPayments({
			pageSize: 25,
			pageToken: pageToken(url),
			orderId
		});
		return { payments: res.payments, next: res.nextPageToken, orderId };
	} catch (e) {
		errorFrom(e);
	}
};
