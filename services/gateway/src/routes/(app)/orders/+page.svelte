<script lang="ts">
	import { enhance } from '$app/forms';
	import FormError from '#lib/components/FormError.svelte';
	import PageHead from '#lib/components/PageHead.svelte';
	import Pager from '#lib/components/Pager.svelte';
	import { formatCents, shortId } from '#lib/format.js';

	let { data, form } = $props();
</script>

<PageHead title="Orders">
	{#if data.filter}
		Showing orders for user <span class="font-mono text-sm">{data.filter}</span>.
		<a class="link" href="/orders">Show everyone’s</a>
	{:else}
		{data.viewer?.isAdmin ? 'Every order across all customers.' : 'Orders you’ve placed.'}
	{/if}
</PageHead>

<div class="grid gap-10 lg:grid-cols-[minmax(0,1fr)_20rem]">
	<section>
		{#if data.orders.length === 0}
			<p class="text-ink-soft">No orders here yet. Place one with the form.</p>
		{:else}
			<table class="w-full text-left">
				<thead class="border-b border-ink text-sm text-ink-soft">
					<tr>
						<th class="py-2 font-medium">Order</th>
						{#if data.viewer?.isAdmin}<th class="py-2 font-medium">Customer</th>{/if}
						<th class="py-2 text-right font-medium">Total</th>
					</tr>
				</thead>
				<tbody class="divide-y divide-line">
					{#each data.orders as order (order.id)}
						<tr class="hover:bg-paper">
							<td class="py-2.5">
								<a class="link font-mono text-sm" href="/orders/{order.id}">{shortId(order.id)}</a>
							</td>
							{#if data.viewer?.isAdmin}
								<td class="py-2.5">
									<a class="font-mono text-sm hover:text-signal" href="?user={order.userId}"
										>{shortId(order.userId)}</a
									>
								</td>
							{/if}
							<td class="py-2.5 text-right font-semibold">{formatCents(order.totalCents)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
			<Pager next={data.next} />
		{/if}
	</section>

	<aside id="new" class="panel h-fit p-5">
		<h2 class="subheading">Place an order</h2>
		<form method="POST" action="?/create" class="mt-4 space-y-4" use:enhance>
			<label class="label">
				Total
				<input class="field" name="total" inputmode="decimal" placeholder="24.99" required />
			</label>
			{#if data.viewer?.isAdmin}
				<label class="label">
					On behalf of (user ID)
					<input
						class="field font-mono text-sm"
						name="userId"
						placeholder="Leave empty for yourself"
					/>
				</label>
			{/if}
			<FormError {form} on="create" />
			{#if form?.created}
				<p class="text-sm text-moss" role="status">
					Order placed. <a class="link" href="/orders/{form.created}">Open it to pay</a>
				</p>
			{/if}
			<button class="btn w-full">Place order</button>
		</form>
	</aside>
</div>
