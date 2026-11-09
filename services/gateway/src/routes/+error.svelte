<script lang="ts">
	import { page } from '$app/state';
</script>

<svelte:head><title>{page.status} · Gateway</title></svelte:head>

<main class="mx-auto max-w-xl px-6 py-24">
	<p class="text-8xl font-black text-signal condensed">{page.status}</p>
	<h1 class="subheading mt-4">{page.error?.message}</h1>
	<p class="mt-6 flex gap-4">
		<a class="link" href="/">Go to the overview</a>
		<a class="link" href={page.url.pathname}>Try again</a>
	</p>
</main>
