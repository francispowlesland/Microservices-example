// Money is int64 minor units end to end; never round-trip it through a float.

/** Parses "12", "12.3" or "12.34" (comma also accepted) into cents. Returns null for anything else. */
export function parseCents(input: string): bigint | null {
	const m = /^\s*(\d{1,10})(?:[.,](\d{1,2}))?\s*$/.exec(input);
	if (!m) return null;
	return BigInt(m[1]) * 100n + BigInt((m[2] ?? '').padEnd(2, '0'));
}

const symbols: Record<string, string> = { GBP: '£', USD: '$', EUR: '€' };

export function formatCents(cents: bigint, currency?: string): string {
	const sign = cents < 0n ? '−' : '';
	const abs = cents < 0n ? -cents : cents;
	const whole = (abs / 100n).toLocaleString('en-GB');
	const frac = (abs % 100n).toString().padStart(2, '0');
	const symbol = currency ? (symbols[currency] ?? `${currency} `) : '';
	return `${sign}${symbol}${whole}.${frac}`;
}

/** Cents as the plain decimal a form field expects, e.g. 1234n -> "12.34". */
export const centsInput = (cents: bigint) =>
	`${cents / 100n}.${(cents % 100n).toString().padStart(2, '0')}`;

export const shortId = (id: string) => id.slice(0, 8);

export function formatTime(ts?: { seconds: bigint }): string {
	if (!ts || ts.seconds === 0n) return '—';
	return new Date(Number(ts.seconds) * 1000).toLocaleString('en-GB', {
		dateStyle: 'medium',
		timeStyle: 'short'
	});
}
