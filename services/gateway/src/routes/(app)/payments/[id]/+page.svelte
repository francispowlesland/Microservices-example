<script lang="ts">
	import { enhance } from '$app/forms';
	import FormError from '#lib/components/FormError.svelte';
	import PageHead from '#lib/components/PageHead.svelte';
	import Status from '#lib/components/Status.svelte';
	import { formatCents, shortId } from '#lib/format.js';
	import { paymentStatus } from '#lib/labels.js';
	import { PaymentStatus } from '#lib/gen/payments/v1/payments_pb.js';

	let { data, form } = $props();
	const status = $derived(paymentStatus[data.payment.status]);
</script>

<PageHead title="Payment {shortId(data.payment.id)}">
	For order <a class="link font-mono text-sm" href="/orders/{data.payment.orderId}"
		>{shortId(data.payment.orderId)}</a
	>
</PageHead>

<div class="grid gap-10 lg:grid-cols-[minmax(0,1fr)_22rem]">
	<dl class="grid h-fit grid-cols-2 gap-6">
		<div>
			<dt class="text-sm text-ink-soft">Amount</dt>
			<dd class="text-5xl font-extrabold condensed">
				{formatCents(data.payment.amountCents, data.payment.currency)}
			</dd>
		</div>
		<div>
			<dt class="text-sm text-ink-soft">Status</dt>
			<dd class="mt-3 text-lg"><Status tone={status[1]}>{status[0]}</Status></dd>
		</div>
		{#if data.payment.failureReason}
			<div class="col-span-2">
				<dt class="text-sm text-ink-soft">Why it was declined</dt>
				<dd class="mt-1 text-brick">{data.payment.failureReason}</dd>
			</div>
		{/if}
		<div class="col-span-2">
			<dt class="text-sm text-ink-soft">Payment ID</dt>
			<dd class="mt-1 font-mono text-sm break-all">{data.payment.id}</dd>
		</div>
	</dl>

	{#if data.viewer?.isAdmin && data.payment.status === PaymentStatus.SUCCEEDED}
		<section class="panel h-fit p-5">
			<h2 class="subheading">Refund this payment</h2>
			<p class="mt-1 text-sm text-ink-soft">Refunds are for the full amount and can’t be undone.</p>
			<form
				method="POST"
				action="?/refund"
				class="mt-4 space-y-4"
				use:enhance={({ cancel }) => {
					if (!confirm('Refund the full amount?')) cancel();
				}}
			>
				<label class="label">
					Reason
					<textarea class="field" name="reason" rows="3" maxlength="500"></textarea>
				</label>
				<FormError {form} on="refund" />
				<button class="btn-danger w-full">Refund in full</button>
			</form>
		</section>
	{:else if form?.refunded}
		<p class="text-moss" role="status">Refunded.</p>
	{/if}
</div>
