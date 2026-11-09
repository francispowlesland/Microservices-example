import { test } from 'node:test';
import assert from 'node:assert/strict';
import { centsInput, formatCents, parseCents } from './format.ts';

test('parseCents', () => {
	assert.equal(parseCents('12'), 1200n);
	assert.equal(parseCents('12.3'), 1230n);
	assert.equal(parseCents(' 12,34 '), 1234n);
	assert.equal(parseCents('0.05'), 5n);
	assert.equal(parseCents('9999999999.99'), 999999999999n);
	for (const bad of ['', '-1', '1.234', 'abc', '1e3', '12345678901', '.5'])
		assert.equal(parseCents(bad), null, bad);
});

test('formatCents', () => {
	assert.equal(formatCents(123456n, 'GBP'), '£1,234.56');
	assert.equal(formatCents(5n), '0.05');
	assert.equal(formatCents(-100n, 'EUR'), '−€1.00');
	assert.equal(centsInput(1205n), '12.05');
});
