<script lang="ts">
	import { enhance } from '$app/forms';
	import FormError from '#lib/components/FormError.svelte';
	import PageHead from '#lib/components/PageHead.svelte';
	import Pager from '#lib/components/Pager.svelte';
	import Status from '#lib/components/Status.svelte';
	import { formatCents } from '#lib/format.js';
	import { stockTone } from '#lib/labels.js';

	let { data, form } = $props();

	let query = $state('');
	const shown = $derived(
		query ? data.items.filter((i) => i.sku.includes(query.trim().toUpperCase())) : data.items
	);
</script>

<PageHead title="Inventory">
	{data.viewer?.isAdmin
		? 'Stock levels for every product. Changes are visible to everyone straight away.'
		: 'Current stock levels for every product.'}
</PageHead>

<div class="grid gap-10 {data.viewer?.isAdmin ? 'lg:grid-cols-[minmax(0,1fr)_20rem]' : ''}">
	<section>
		{#if data.items.length === 0}
			<p class="text-ink-soft">
				No products tracked yet.{data.viewer?.isAdmin ? ' Add the first one with the form.' : ''}
			</p>
		{:else}
			<label class="label mb-4 max-w-xs">
				Filter this page by SKU
				<input class="field" type="search" bind:value={query} />
			</label>
			<table class="w-full text-left">
				<thead class="border-b border-ink text-sm text-ink-soft">
					<tr>
						<th class="py-2 font-medium">SKU</th>
						<th class="py-2 font-medium">Availability</th>
						<th class="py-2 text-right font-medium">Price</th>
						<th class="py-2 text-right font-medium">Quantity</th>
					</tr>
				</thead>
				<tbody class="divide-y divide-line">
					{#each shown as item (item.id)}
						{@const [label, tone] = stockTone(item.quantity)}
						<tr class="hover:bg-paper">
							<td class="py-2.5">
								{#if data.viewer?.isAdmin}
									<a class="link font-mono text-sm" href="/inventory/{item.id}">{item.sku}</a>
								{:else}
									<span class="font-mono text-sm">{item.sku}</span>
								{/if}
							</td>
							<td class="py-2.5"><Status {tone}>{label}</Status></td>
							<td class="py-2.5 text-right">{formatCents(item.priceCents)}</td>
							<td class="py-2.5 text-right font-semibold"
								>{item.quantity.toLocaleString('en-GB')}</td
							>
						</tr>
					{:else}
						<tr
							><td colspan="4" class="py-4 text-ink-soft">No SKU on this page matches “{query}”.</td
							></tr
						>
					{/each}
				</tbody>
			</table>
			<Pager next={data.next} />
		{/if}
	</section>

	{#if data.viewer?.isAdmin}
		<aside class="panel h-fit p-5">
			<h2 class="subheading">Add a product</h2>
			<form method="POST" action="?/create" class="mt-4 space-y-4" use:enhance>
				<label class="label">
					SKU
					<input
						class="field font-mono uppercase"
						name="sku"
						required
						maxlength="64"
						pattern="[A-Za-z0-9][A-Za-z0-9._\-]*"
						value={form?.on === 'create' && !form.created ? (form.sku ?? '') : ''}
					/>
				</label>
				<label class="label">
					Quantity
					<input class="field" name="quantity" type="number" min="0" step="1" value="0" required />
				</label>
				<label class="label">
					Price
					<input class="field" name="price" inputmode="decimal" placeholder="4.99" required />
				</label>
				<FormError {form} on="create" />
				{#if form?.created}<p class="text-sm text-moss" role="status">Added {form.created}.</p>{/if}
				<button class="btn w-full">Add product</button>
			</form>
		</aside>
	{/if}
</div>
