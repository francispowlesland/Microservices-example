<script lang="ts">
	import type { Snippet } from 'svelte';
	let { title, children, actions }: { title: string; children?: Snippet; actions?: Snippet } =
		$props();
</script>

<svelte:head><title>{title} · Gateway</title></svelte:head>

<header class="mb-8 flex flex-wrap items-end justify-between gap-4 border-b-2 border-ink pb-4">
	<div>
		<h1 class="heading">{title}</h1>
		{#if children}<div class="mt-2 max-w-prose text-ink-soft">{@render children()}</div>{/if}
	</div>
	{#if actions}<div class="flex gap-2">{@render actions()}</div>{/if}
</header>
