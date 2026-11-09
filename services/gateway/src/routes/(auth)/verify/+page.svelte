<script lang="ts">
	import FormError from '#lib/components/FormError.svelte';
	let { data, form } = $props();
</script>

<svelte:head><title>Verify your email · Gateway</title></svelte:head>

<h1 class="heading">Verify your email</h1>
<p class="mt-4 text-ink-soft">
	Confirm the token from your verification email to activate your account.
</p>
<form method="POST" class="mt-8 space-y-5">
	<label class="label">
		Verification token
		<input class="field font-mono" name="token" required value={data.token} autocomplete="off" />
	</label>
	<FormError {form} />
	<button class="btn w-full">Verify email</button>
</form>
<p class="mt-8"><a class="link" href="/login">Back to sign in</a></p>
