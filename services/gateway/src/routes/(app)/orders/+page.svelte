<script lang="ts">
	import { enhance } from '$app/forms';
	import FormError from '#lib/components/FormError.svelte';
	import PageHead from '#lib/components/PageHead.svelte';
	import Pager from '#lib/components/Pager.svelte';
	import { formatCents, shortId } from '#lib/format.js';

	let { data, form } = $props();

	let rows = $state([0]);
	let nextRow = 1;
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
			{#each rows as row (row)}
				<div class="grid grid-cols-[1fr_4.5rem_auto] items-end gap-2">
					<label class="label">
						Product
						<select class="field" name="sku">
							<option value="">Choose…</option>
							{#each data.products as p (p.id)}
								<option value={p.sku} disabled={p.quantity < 1}>
									{p.sku} · {formatCents(p.priceCents)}{p.quantity < 1 ? ' (out of stock)' : ''}
								</option>
							{/each}
						</select>
					</label>
					<label class="label">
						Qty
						<input class="field" name="quantity" type="number" min="1" step="1" value="1" />
					</label>
					{#if rows.length > 1}
						<button
							type="button"
							class="btn-quiet"
							aria-label="Remove line"
							onclick={() => (rows = rows.filter((r) => r !== row))}>✕</button
						>
					{/if}
				</div>
			{/each}
			<button type="button" class="btn-quiet w-full" onclick={() => rows.push(nextRow++)}>
				Add another product
			</button>
			<p class="text-sm text-ink-soft">The total is worked out from current prices.</p>
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
