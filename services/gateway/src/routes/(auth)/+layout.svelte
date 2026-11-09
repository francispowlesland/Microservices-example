<script lang="ts">
	let { children } = $props();
</script>

<div class="grid min-h-dvh lg:grid-cols-[1fr_minmax(0,32rem)]">
	<aside
		class="relative hidden overflow-hidden bg-ink p-12 text-fog lg:flex lg:flex-col lg:justify-between"
	>
		<p class="text-lg font-bold semi-condensed">Gateway</p>
		<div>
			<p
				class="text-[7.5rem] leading-[0.85] font-black tracking-tight condensed"
				aria-hidden="true"
			>
				Users<br /><span class="text-fog/60">Orders</span><br /><span class="text-fog/35"
					>Payments</span
				><br /><span class="text-fog/15">Inventory</span>
			</p>
			<p class="mt-8 max-w-sm text-fog/70">
				One sign-in for every service: manage accounts, place and track orders, take payments and
				keep stock up to date.
			</p>
		</div>
	</aside>
	<main class="flex flex-col justify-center px-6 py-12 sm:px-12">
		<p class="mb-10 text-lg font-bold semi-condensed lg:hidden">Gateway</p>
		{@render children()}
	</main>
</div>
