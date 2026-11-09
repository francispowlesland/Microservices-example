<script lang="ts">
	import PageHead from '#lib/components/PageHead.svelte';
	import Pager from '#lib/components/Pager.svelte';
	import Status from '#lib/components/Status.svelte';
	import { formatCents, shortId } from '#lib/format.js';
	import { paymentStatus } from '#lib/labels.js';

	let { data } = $props();
</script>

<PageHead title="Payments">
	{#if data.orderId}
		Payments for order <a class="link font-mono text-sm" href="/orders/{data.orderId}"
			>{shortId(data.orderId)}</a
		>. <a class="link" href="/payments">Show all</a>
	{:else}
		{data.viewer?.isAdmin
			? 'Every payment in the ledger. Payments can’t be edited, only refunded.'
			: 'Payments you’ve made. To pay, open an order.'}
	{/if}
</PageHead>

{#if data.payments.length === 0}
	<p class="text-ink-soft">
		No payments yet. Open one of your <a class="link" href="/orders">orders</a> to pay for it.
	</p>
{:else}
	<table class="w-full text-left">
		<thead class="border-b border-ink text-sm text-ink-soft">
			<tr>
				<th class="py-2 font-medium">Payment</th>
				<th class="py-2 font-medium">Order</th>
				<th class="py-2 font-medium">Status</th>
				<th class="py-2 text-right font-medium">Amount</th>
			</tr>
		</thead>
		<tbody class="divide-y divide-line">
			{#each data.payments as p (p.id)}
				{@const [label, tone] = paymentStatus[p.status]}
				<tr class="hover:bg-paper">
					<td class="py-2.5">
						<a class="link font-mono text-sm" href="/payments/{p.id}">{shortId(p.id)}</a>
					</td>
					<td class="py-2.5">
						<a class="font-mono text-sm hover:text-signal" href="/orders/{p.orderId}"
							>{shortId(p.orderId)}</a
						>
					</td>
					<td class="py-2.5"><Status {tone}>{label}</Status></td>
					<td class="py-2.5 text-right font-semibold">{formatCents(p.amountCents, p.currency)}</td>
				</tr>
			{/each}
		</tbody>
	</table>
	<Pager next={data.next} />
{/if}
