import { Code, ConnectError } from '@connectrpc/connect';
import { fail, redirect } from '@sveltejs/kit';
import { clients } from '#lib/server/clients.js';
import { failFrom, text } from '#lib/server/rpc.js';
import { setTokens } from '#lib/server/session.js';
import type { Actions, PageServerLoad } from './$types';

// Only same-site paths, so `?next=` can't bounce someone to another origin.
const safeNext = (next: string | null) =>
	next && next.startsWith('/') && !next.startsWith('//') && !next.startsWith('/\\') ? next : '/';

export const load: PageServerLoad = ({ url }) => ({
	notice: url.searchParams.get('notice')
});

export const actions: Actions = {
	default: async ({ request, cookies, url }) => {
		const data = await request.formData();
		const identifier = text(data, 'identifier');
		const password = String(data.get('password') ?? '');
		if (!identifier || !password)
			return fail(400, { identifier, message: 'Enter your email or username and your password.' });

		try {
			const res = await clients().auth.login({ identifier, password });
			if (!res.tokens) throw new Error('login returned no tokens');
			setTokens(cookies, res.tokens);
		} catch (e) {
			const c = ConnectError.from(e);
			if (c.code === Code.Unauthenticated)
				return fail(401, { identifier, message: 'That email, username or password is wrong.' });
			if (c.code === Code.FailedPrecondition)
				return fail(409, {
					identifier,
					message: 'Verify your email first. Use the link we sent when you signed up.'
				});
			if (c.code === Code.PermissionDenied)
				return fail(403, { identifier, message: 'This account is suspended.' });
			const f = failFrom(e);
			return fail(f.status, { identifier, ...f.data });
		}
		redirect(303, safeNext(url.searchParams.get('next')));
	}
};
