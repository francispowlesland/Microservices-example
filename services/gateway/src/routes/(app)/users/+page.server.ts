import { api } from '#lib/server/clients.js';
import { errorFrom, pageToken } from '#lib/server/rpc.js';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ locals, url }) => {
	try {
		const res = await api(locals).users.listUsers({ pageSize: 50, pageToken: pageToken(url) });
		return { users: res.users, next: res.nextPageToken };
	} catch (e) {
		errorFrom(e);
	}
};
