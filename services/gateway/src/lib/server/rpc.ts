import { Code, ConnectError } from '@connectrpc/connect';
import { error, fail, redirect } from '@sveltejs/kit';

const statusFor: Partial<Record<Code, number>> = {
	[Code.InvalidArgument]: 400,
	[Code.Unauthenticated]: 401,
	[Code.PermissionDenied]: 403,
	[Code.NotFound]: 404,
	[Code.AlreadyExists]: 409,
	[Code.FailedPrecondition]: 409,
	[Code.Unavailable]: 503,
	[Code.DeadlineExceeded]: 504
};

export function messageFor(e: ConnectError): string {
	switch (e.code) {
		case Code.Unavailable:
		case Code.DeadlineExceeded:
			return 'That service isn’t responding. Try again in a moment.';
		case Code.PermissionDenied:
			return e.rawMessage || 'You don’t have access to this.';
		case Code.NotFound:
			return e.rawMessage || 'We couldn’t find that.';
		case Code.Internal:
		case Code.Unknown:
			return 'Something went wrong on our side. Try again.';
		default: {
			const m = e.rawMessage || 'That request didn’t work.';
			return m[0].toUpperCase() + m.slice(1);
		}
	}
}

/** For loads: turn a gRPC failure into the matching error page (or a trip to sign in). */
export function errorFrom(e: unknown): never {
	const c = ConnectError.from(e);
	if (c.code === Code.Unauthenticated) redirect(303, '/login');
	error(statusFor[c.code] ?? 500, messageFor(c));
}

/** For actions: turn a gRPC failure into a form message. `on` says which form on the page failed. */
export function failFrom(e: unknown, on?: string) {
	const c = ConnectError.from(e);
	if (c.code === Code.Unauthenticated) redirect(303, '/login');
	return fail(statusFor[c.code] ?? 500, { on, message: messageFor(c) });
}

export const text = (data: FormData, key: string) => String(data.get(key) ?? '').trim();

export const pageToken = (url: URL) => url.searchParams.get('page') ?? '';
