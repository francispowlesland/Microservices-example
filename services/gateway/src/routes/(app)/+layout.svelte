<script lang="ts">
	import { page } from '$app/state';

	let { data, children } = $props();

	const links = $derived([
		{ href: '/', label: 'Overview' },
		{ href: '/orders', label: 'Orders' },
		{ href: '/payments', label: 'Payments' },
		{ href: '/inventory', label: 'Inventory' },
		...(data.viewer?.isAdmin ? [{ href: '/users', label: 'Users' }] : [])
	]);

	const current = (href: string) =>
		href === '/' ? page.url.pathname === '/' : page.url.pathname.startsWith(href);
</script>

<a
	href="#main"
	class="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-10 focus:bg-paper focus:p-2"
	>Skip to content</a
>

<header class="border-b border-line bg-paper">
	<div class="mx-auto flex max-w-6xl flex-wrap items-center gap-x-8 gap-y-2 px-6 py-3">
		<a href="/" class="text-lg font-extrabold semi-condensed">Gateway</a>
		<nav class="order-last -mx-3 flex w-full gap-1 overflow-x-auto sm:order-none sm:w-auto">
			{#each links as link (link.href)}
				<a
					href={link.href}
					aria-current={current(link.href) ? 'page' : undefined}
					class="rounded-[4px] px-3 py-1.5 text-[15px] font-medium whitespace-nowrap text-ink-soft hover:text-ink aria-[current=page]:bg-ink aria-[current=page]:text-paper"
				>
					{link.label}
				</a>
			{/each}
		</nav>
		<div class="ml-auto flex items-center gap-4 text-[15px]">
			{#if data.viewer?.isAdmin}
				<span class="rounded-full border border-signal px-2 text-xs font-semibold text-signal"
					>Admin</span
				>
			{/if}
			<a href="/account" class="font-medium hover:text-signal">Account</a>
			<form method="POST" action="/logout">
				<button class="font-medium text-ink-soft hover:text-ink">Sign out</button>
			</form>
		</div>
	</div>
</header>

<main id="main" class="mx-auto max-w-6xl px-6 py-10">
	{@render children()}
</main>
