<script lang="ts">
	import { enhance } from '$app/forms';
	import FormError from '#lib/components/FormError.svelte';
	import PageHead from '#lib/components/PageHead.svelte';
	import Status from '#lib/components/Status.svelte';
	import { stockTone } from '#lib/labels.js';

	let { data, form } = $props();
	const tone = $derived(stockTone(data.item.quantity));
</script>

<PageHead title={data.item.sku}>
	<a class="link" href="/inventory">All products</a>
</PageHead>

<div class="grid gap-10 lg:grid-cols-[minmax(0,1fr)_22rem]">
	<dl class="grid h-fit grid-cols-2 gap-6">
		<div>
			<dt class="text-sm text-ink-soft">On hand</dt>
			<dd class="text-6xl font-extrabold condensed">
				{data.item.quantity.toLocaleString('en-GB')}
			</dd>
		</div>
		<div>
			<dt class="text-sm text-ink-soft">Availability</dt>
			<dd class="mt-4 text-lg"><Status tone={tone[1]}>{tone[0]}</Status></dd>
		</div>
	</dl>

	{#if data.viewer?.isAdmin}
		<section class="panel h-fit p-5">
			<h2 class="subheading">Edit product</h2>
			<p class="mt-1 text-sm text-ink-soft">
				This sets the exact quantity. If someone else changes it at the same time, the last save
				wins.
			</p>
			<form
				method="POST"
				action="?/update"
				class="mt-4 space-y-4"
				use:enhance={() =>
					({ update }) =>
						update({ reset: false })}
			>
				<label class="label">
					SKU
					<input
						class="field font-mono uppercase"
						name="sku"
						required
						maxlength="64"
						value={data.item.sku}
					/>
				</label>
				<label class="label">
					Quantity
					<input
						class="field"
						name="quantity"
						type="number"
						min="0"
						step="1"
						required
						value={data.item.quantity}
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
					if (!confirm(`Delete ${data.item.sku}?`)) cancel();
				}}
			>
				<FormError {form} on="delete" />
				<button class="btn-danger w-full">Delete product</button>
			</form>
		</section>
	{/if}
</div>
