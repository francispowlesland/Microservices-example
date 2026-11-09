import { Code, ConnectError } from '@connectrpc/connect';
import type { Cookies } from '@sveltejs/kit';
import type { Tokens } from '#lib/gen/user/v1/user_pb.js';
import { clients } from './clients';

const ACCESS = 'ob_access';
const REFRESH = 'ob_refresh';

export type Session = { userId: string; roles: string[]; isAdmin: boolean; accessToken: string };

// The signature is not checked here: every backend verifies the JWT itself. The gateway only
// reads the claims to decide what to show, so a forged cookie gets a different UI, not more access.
function readClaims(token: string) {
	try {
		const p = JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString());
		if (typeof p.sub !== 'string' || typeof p.exp !== 'number') return null;
		return {
			sub: p.sub as string,
			exp: p.exp as number,
			roles: Array.isArray(p.roles) ? p.roles : []
		};
	} catch {
		return null;
	}
}

function toSession(accessToken: string): Session | null {
	const c = readClaims(accessToken);
	if (!c || c.exp * 1000 < Date.now() + 30_000) return null;
	return { userId: c.sub, roles: c.roles, isAdmin: c.roles.includes('admin'), accessToken };
}

const expires = (ts?: { seconds: bigint }) =>
	ts ? new Date(Number(ts.seconds) * 1000) : undefined;

export function setTokens(cookies: Cookies, t: Tokens) {
	const opts = { path: '/', httpOnly: true, sameSite: 'lax' } as const;
	cookies.set(ACCESS, t.accessToken, { ...opts, expires: expires(t.accessTokenExpiresAt) });
	cookies.set(REFRESH, t.refreshToken, { ...opts, expires: expires(t.refreshTokenExpiresAt) });
}

export function clearTokens(cookies: Cookies) {
	cookies.delete(ACCESS, { path: '/' });
	cookies.delete(REFRESH, { path: '/' });
}

export const refreshTokenOf = (cookies: Cookies) => cookies.get(REFRESH);

// Refresh tokens rotate, and presenting a used one revokes the whole family. Parallel requests
// that all find an expired access token must therefore share a single refresh call.
// shortcut: dedupe is per process; with several gateway replicas use sticky sessions or a shared store.
const inflight = new Map<string, Promise<Tokens | undefined>>();

function refresh(refreshToken: string) {
	let p = inflight.get(refreshToken);
	if (!p) {
		p = clients()
			.auth.refreshToken({ refreshToken })
			.then((r) => r.tokens)
			.finally(() => setTimeout(() => inflight.delete(refreshToken), 10_000));
		inflight.set(refreshToken, p);
	}
	return p;
}

export async function resolveSession(cookies: Cookies): Promise<Session | null> {
	const access = cookies.get(ACCESS);
	const current = access ? toSession(access) : null;
	if (current) return current;

	const refreshToken = cookies.get(REFRESH);
	if (!refreshToken) return null;
	try {
		const tokens = await refresh(refreshToken);
		if (!tokens) return null;
		setTokens(cookies, tokens);
		return toSession(tokens.accessToken);
	} catch (e) {
		// Only drop the cookies when the token is really dead; an outage shouldn't sign everyone out.
		if (ConnectError.from(e).code === Code.Unauthenticated) clearTokens(cookies);
		return null;
	}
}
