<script lang="ts">
	import { enhance } from '$app/forms';
	import FormError from '#lib/components/FormError.svelte';

	let { form } = $props();
	let busy = $state(false);
</script>

<svelte:head><title>Create an account · Gateway</title></svelte:head>

{#if form?.created}
	<h1 class="heading">Check your inbox</h1>
	<p class="mt-6 text-lg">
		We sent a verification link to <strong>{form.created}</strong>. Open it to activate your
		account, then sign in.
	</p>
	<p class="mt-4 text-sm text-ink-soft">
		Running locally? The users service writes the link’s token to its log. Paste it on the
		<a class="link" href="/verify">verification page</a>.
	</p>
{:else}
	<h1 class="heading">Create an account</h1>
	<form
		method="POST"
		class="mt-8 space-y-5"
		use:enhance={() => {
			busy = true;
			return async ({ update }) => {
				await update({ reset: false });
				busy = false;
			};
		}}
	>
		<label class="label">
			Full name
			<input
				class="field"
				name="name"
				autocomplete="name"
				required
				maxlength="100"
				value={form?.name ?? ''}
			/>
		</label>
		<label class="label">
			Username
			<input
				class="field"
				name="username"
				autocomplete="username"
				required
				minlength="3"
				maxlength="32"
				pattern="[a-zA-Z0-9_]+"
				aria-describedby="username-hint"
				value={form?.username ?? ''}
			/>
			<span id="username-hint" class="mt-1 block text-xs font-normal">
				3 to 32 letters, numbers or underscores.
			</span>
		</label>
		<label class="label">
			Email
			<input
				class="field"
				type="email"
				name="email"
				autocomplete="email"
				required
				value={form?.email ?? ''}
			/>
		</label>
		<label class="label">
			Password
			<input
				class="field"
				type="password"
				name="password"
				autocomplete="new-password"
				required
				minlength="12"
				maxlength="128"
				aria-describedby="password-hint"
			/>
			<span id="password-hint" class="mt-1 block text-xs font-normal">
				At least 12 characters. A short phrase works well.
			</span>
		</label>
		<FormError {form} />
		<button class="btn w-full" disabled={busy}>
			{busy ? 'Creating account…' : 'Create account'}
		</button>
	</form>
	<p class="mt-8 text-ink-soft">
		Already have an account? <a class="link" href="/login">Sign in</a>
	</p>
{/if}
