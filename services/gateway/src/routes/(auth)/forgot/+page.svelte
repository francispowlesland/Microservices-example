<script lang="ts">
	import FormError from '#lib/components/FormError.svelte';
	let { form } = $props();
</script>

<svelte:head><title>Reset your password · Gateway</title></svelte:head>

<h1 class="heading">Reset your password</h1>
{#if form?.sent}
	<p class="mt-6 text-lg">
		If that email has an account, a reset link is on its way. It works for one hour.
	</p>
	<p class="mt-4">
		<a class="link" href="/reset">I have a reset token</a>
	</p>
{:else}
	<p class="mt-4 text-ink-soft">We’ll email you a link to choose a new password.</p>
	<form method="POST" class="mt-8 space-y-5">
		<label class="label">
			Email
			<input class="field" type="email" name="email" autocomplete="email" required />
		</label>
		<FormError {form} />
		<button class="btn w-full">Send reset link</button>
	</form>
{/if}
<p class="mt-8"><a class="link" href="/login">Back to sign in</a></p>
