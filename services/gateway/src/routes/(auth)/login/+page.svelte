<script lang="ts">
	import { enhance } from '$app/forms';
	import { page } from '$app/state';
	import FormError from '#lib/components/FormError.svelte';

	let { data, form } = $props();
	let busy = $state(false);

	const notices: Record<string, string> = {
		verified: 'Your email is verified. Sign in to continue.',
		reset: 'Your password has been changed. Sign in with the new one.',
		password: 'Your password has been changed and other sessions were signed out. Sign in again.'
	};
</script>

<svelte:head><title>Sign in · Gateway</title></svelte:head>

<h1 class="heading">Sign in</h1>
{#if data.notice && notices[data.notice]}
	<p class="mt-6 rounded-[4px] border-l-4 border-moss bg-moss/5 px-3 py-2 text-sm text-moss">
		{notices[data.notice]}
	</p>
{/if}

<form
	method="POST"
	action="?{page.url.searchParams}"
	class="mt-8 space-y-5"
	use:enhance={() => {
		busy = true;
		return async ({ update }) => {
			await update();
			busy = false;
		};
	}}
>
	<label class="label">
		Email or username
		<input
			class="field"
			name="identifier"
			autocomplete="username"
			required
			value={form?.identifier ?? ''}
		/>
	</label>
	<label class="label">
		<span class="flex justify-between">
			Password <a class="link text-sm" href="/forgot">Forgotten it?</a>
		</span>
		<input class="field" type="password" name="password" autocomplete="current-password" required />
	</label>
	<FormError {form} />
	<button class="btn w-full" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
</form>

<p class="mt-8 text-ink-soft">New here? <a class="link" href="/signup">Create an account</a></p>
