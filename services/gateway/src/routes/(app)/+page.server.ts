import { api } from '#lib/server/clients.js';
import { messageFor } from '#lib/server/rpc.js';
import { ConnectError } from '@connectrpc/connect';
import type { PageServerLoad } from './$types';

// Each service is loaded independently, so one being down doesn't blank the whole overview.
const settle = <T>(p: Promise<T>) =>
	p.then(
		(value) => ({ ok: true as const, value }),
		(e) => ({ ok: false as const, message: messageFor(ConnectError.from(e)) })
	);

export const load: PageServerLoad = async ({ locals }) => {
	const c = api(locals);
	const session = locals.session!;
	const [orders, payments, inventory, users] = await Promise.all([
		settle(c.orders.listOrders({ pageSize: 5 })),
		settle(c.payments.listPayments({ pageSize: 5 })),
		settle(c.inventory.listInventoryItems({ pageSize: 200 })),
		session.isAdmin
			? settle(c.users.listUsers({ pageSize: 5 }))
			: settle(c.users.getUser({ id: session.userId }))
	]);
	return { orders, payments, inventory, users };
};
