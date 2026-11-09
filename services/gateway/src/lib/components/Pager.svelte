<script lang="ts">
	import { page } from '$app/state';

	let { next }: { next: string } = $props();

	const href = (token: string | null) => {
		// eslint-disable-next-line svelte/prefer-svelte-reactivity -- throwaway value, not state
		const params = new URLSearchParams(page.url.search);
		if (token) params.set('page', token);
		else params.delete('page');
		const qs = params.toString();
		return qs ? `?${qs}` : page.url.pathname;
	};
</script>

{#if next || page.url.searchParams.has('page')}
	<nav class="mt-6 flex gap-3" aria-label="Pages">
		{#if page.url.searchParams.has('page')}
			<a class="btn-quiet" href={href(null)}>Back to the start</a>
		{/if}
		{#if next}
			<a class="btn-quiet" href={href(next)}>Next page</a>
		{/if}
	</nav>
{/if}
