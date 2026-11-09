<script lang="ts">
	import { enhance } from '$app/forms';
	import FormError from '#lib/components/FormError.svelte';
	import PageHead from '#lib/components/PageHead.svelte';
	import Status from '#lib/components/Status.svelte';
	import { centsInput, formatCents, shortId } from '#lib/format.js';
	import { paymentStatus } from '#lib/labels.js';
	import { PaymentStatus } from '#lib/gen/payments/v1/payments_pb.js';

	let { data, form } = $props();

	const paid = $derived(
		data.payments
			.filter((p) => p.status === PaymentStatus.SUCCEEDED)
			.reduce((s, p) => s + p.amountCents, 0n)
	);
</script>

<PageHead title="Order {shortId(data.order.id)}">
	<a class="link" href="/orders">All orders</a>
</PageHead>

<div class="grid gap-10 lg:grid-cols-[minmax(0,1fr)_22rem]">
	<div>
		<dl class="grid grid-cols-2 gap-6 sm:grid-cols-3">
			<div>
				<dt class="text-sm text-ink-soft">Total</dt>
				<dd class="text-4xl font-extrabold condensed">{formatCents(data.order.totalCents)}</dd>
			</div>
			<div>
				<dt class="text-sm text-ink-soft">Paid so far</dt>
				<dd
					class="text-4xl font-extrabold condensed {paid >= data.order.totalCents
						? 'text-moss'
						: ''}"
				>
					{formatCents(paid)}
				</dd>
			</div>
			<div class="col-span-2 sm:col-span-1">
				<dt class="text-sm text-ink-soft">Customer</dt>
				<dd class="mt-2 font-mono text-sm break-all">{data.order.userId}</dd>
			</div>
		</dl>

		<h2 class="subheading mt-12 border-b border-ink pb-2">Payments</h2>
		{#if data.payments.length === 0}
			<p class="mt-4 text-ink-soft">Nothing paid against this order yet.</p>
		{:else}
			<ul class="divide-y divide-line">
				{#each data.payments as p (p.id)}
					{@const [label, tone] = paymentStatus[p.status]}
					<li>
						<a
							href="/payments/{p.id}"
							class="grid grid-cols-[1fr_auto_8rem] items-center gap-4 py-2.5 hover:text-signal"
						>
							<span class="font-mono text-sm">{shortId(p.id)}</span>
							<span class="font-semibold">{formatCents(p.amountCents, p.currency)}</span>
							<Status {tone}>{label}</Status>
						</a>
						{#if p.failureReason}<p class="-mt-1 pb-2 text-sm text-brick">{p.failureReason}</p>{/if}
					</li>
				{/each}
			</ul>
		{/if}
	</div>

	<aside class="space-y-6">
		<section class="panel p-5">
			<h2 class="subheading">Pay for this order</h2>
			<form method="POST" action="?/pay" class="mt-4 space-y-4" use:enhance>
				<input type="hidden" name="key" value={data.idempotencyKey} />
				<div class="grid grid-cols-[1fr_6rem] gap-3">
					<label class="label">
						Amount
						<input
							class="field"
							name="amount"
							inputmode="decimal"
							required
							value={centsInput(data.order.totalCents > paid ? data.order.totalCents - paid : 0n)}
						/>
					</label>
					<label class="label">
						Currency
						<select class="field" name="currency">
							{#each data.currencies as c (c)}<option>{c}</option>{/each}
						</select>
					</label>
				</div>
				<FormError {form} on="pay" />
				{#if form?.paymentId}
					<p class="text-sm" role="status">
						Payment recorded. <a class="link" href="/payments/{form.paymentId}">See the result</a>
					</p>
				{/if}
				<button class="btn w-full">Take payment</button>
			</form>
		</section>

		{#if data.viewer?.isAdmin}
			<section class="panel p-5">
				<h2 class="subheading">Edit order</h2>
				<form
					method="POST"
					action="?/update"
					class="mt-4 space-y-4"
					use:enhance={() =>
						({ update }) =>
							update({ reset: false })}
				>
					<label class="label">
						Total
						<input
							class="field"
							name="total"
							inputmode="decimal"
							required
							value={centsInput(data.order.totalCents)}
						/>
					</label>
					<label class="label">
						Customer (user ID)
						<input
							class="field font-mono text-sm"
							name="userId"
							required
							value={data.order.userId}
						/>
					</label>
					<FormError {form} on="update" />
					{#if form?.saved}<p class="text-sm text-moss" role="status">Changes saved.</p>{/if}
					<button class="btn-quiet w-full">Save changes</button>
				</form>
				<form
					method="POST"
					action="?/delete"
					class="mt-6 border-t border-line pt-4"
					use:enhance={({ cancel }) => {
						if (!confirm('Delete this order? Its payments stay in the ledger.')) cancel();
					}}
				>
					<FormError {form} on="delete" />
					<button class="btn-danger w-full">Delete order</button>
				</form>
			</section>
		{/if}
	</aside>
</div>
