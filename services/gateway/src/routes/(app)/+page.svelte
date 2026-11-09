<script lang="ts">
	import Status from '#lib/components/Status.svelte';
	import { formatCents, shortId } from '#lib/format.js';
	import { paymentStatus, stockTone, userStatus } from '#lib/labels.js';

	let { data } = $props();

	const stock = $derived.by(() => {
		if (!data.inventory.ok) return null;
		const items = data.inventory.value.items;
		return {
			items,
			out: items.filter((i) => i.quantity === 0).length,
			low: items.filter((i) => i.quantity > 0 && i.quantity < 10).length,
			more: !!data.inventory.value.nextPageToken
		};
	});
</script>

<svelte:head><title>Overview · Gateway</title></svelte:head>

{#snippet down(message: string)}
	<p class="flex items-start gap-2 text-brick">
		<span class="mt-2 size-2 shrink-0 rounded-full bg-brick"></span>{message}
	</p>
{/snippet}

{#snippet band(name: string, href: string, tone: string)}
	<a {href} class="group block">
		<h2
			class="text-6xl leading-[0.9] font-black tracking-tight condensed {tone} transition-colors group-hover:text-signal sm:text-7xl"
		>
			{name}
		</h2>
	</a>
{/snippet}

<h1 class="sr-only">Overview</h1>

<div class="divide-y divide-line border-y-2 border-ink">
	<section class="grid gap-6 py-8 md:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
		{@render band('Orders', '/orders', 'text-ink')}
		<div>
			{#if !data.orders.ok}
				{@render down(data.orders.message)}
			{:else if data.orders.value.orders.length === 0}
				<p class="text-ink-soft">No orders yet.</p>
				<a class="btn mt-4" href="/orders#new">Place an order</a>
			{:else}
				<ul class="divide-y divide-line">
					{#each data.orders.value.orders as order (order.id)}
						<li>
							<a href="/orders/{order.id}" class="flex justify-between py-2 hover:text-signal">
								<span>Order <span class="font-mono text-sm">{shortId(order.id)}</span></span>
								<span class="font-semibold">{formatCents(order.totalCents)}</span>
							</a>
						</li>
					{/each}
				</ul>
				<a class="link mt-3 inline-block text-sm" href="/orders">All orders</a>
			{/if}
		</div>
	</section>

	<section class="grid gap-6 py-8 md:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
		{@render band('Payments', '/payments', 'text-ink/75')}
		<div>
			{#if !data.payments.ok}
				{@render down(data.payments.message)}
			{:else if data.payments.value.payments.length === 0}
				<p class="text-ink-soft">No payments yet. Pay for an order from its page.</p>
			{:else}
				<ul class="divide-y divide-line">
					{#each data.payments.value.payments as p (p.id)}
						{@const [label, tone] = paymentStatus[p.status]}
						<li>
							<a
								href="/payments/{p.id}"
								class="grid grid-cols-[1fr_auto_8rem] items-center gap-4 py-2 hover:text-signal"
							>
								<span>For order <span class="font-mono text-sm">{shortId(p.orderId)}</span></span>
								<span class="font-semibold">{formatCents(p.amountCents, p.currency)}</span>
								<Status {tone}>{label}</Status>
							</a>
						</li>
					{/each}
				</ul>
				<a class="link mt-3 inline-block text-sm" href="/payments">All payments</a>
			{/if}
		</div>
	</section>

	<section class="grid gap-6 py-8 md:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
		{@render band('Inventory', '/inventory', 'text-ink/55')}
		<div>
			{#if !data.inventory.ok}
				{@render down(data.inventory.message)}
			{:else if stock}
				<p class="text-lg">
					<strong>{stock.items.length}{stock.more ? '+' : ''}</strong>
					{stock.items.length === 1 ? 'product' : 'products'} tracked.
					{#if stock.out || stock.low}
						<strong class="text-brick">{stock.out} out of stock</strong>,
						<strong class="text-amber">{stock.low} running low</strong>.
					{:else if stock.items.length}
						Everything is in stock.
					{/if}
				</p>
				{#if stock.items.length}
					<!-- One tick per product, sorted by quantity: a glance tells you how much is short. -->
					<div class="mt-4 flex h-10 items-end gap-px" aria-hidden="true">
						{#each [...stock.items]
							.sort((a, b) => a.quantity - b.quantity)
							.slice(0, 120) as item (item.id)}
							{@const [, tone] = stockTone(item.quantity)}
							<span
								class="w-1.5 rounded-t-[1px] {tone === 'bad'
									? 'h-1 bg-brick'
									: tone === 'warn'
										? 'h-4 bg-amber'
										: 'h-10 bg-moss/70'}"
								title="{item.sku}: {item.quantity}"
							></span>
						{/each}
					</div>
				{/if}
				<a class="link mt-3 inline-block text-sm" href="/inventory">Check stock</a>
			{/if}
		</div>
	</section>

	<section class="grid gap-6 py-8 md:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
		{@render band(
			data.viewer?.isAdmin ? 'Users' : 'Account',
			data.viewer?.isAdmin ? '/users' : '/account',
			'text-ink/35'
		)}
		<div>
			{#if !data.users.ok}
				{@render down(data.users.message)}
			{:else if 'users' in data.users.value}
				<ul class="divide-y divide-line">
					{#each data.users.value.users as u (u.id)}
						{@const [label, tone] = userStatus[u.status]}
						<li>
							<a href="/users/{u.id}" class="flex justify-between gap-4 py-2 hover:text-signal">
								<span>{u.name} <span class="text-ink-soft">@{u.username}</span></span>
								<Status {tone}>{label}</Status>
							</a>
						</li>
					{/each}
				</ul>
				<a class="link mt-3 inline-block text-sm" href="/users">All users</a>
			{:else if data.users.value.user}
				{@const u = data.users.value.user}
				<p class="text-lg">Signed in as <strong>{u.name}</strong> (@{u.username}).</p>
				<p class="text-ink-soft">{u.email}</p>
				<a class="link mt-3 inline-block text-sm" href="/account">Manage your account</a>
			{/if}
		</div>
	</section>
</div>
