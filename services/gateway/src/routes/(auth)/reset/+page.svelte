<script lang="ts">
	import FormError from '#lib/components/FormError.svelte';
	let { data, form } = $props();
</script>

<svelte:head><title>Choose a new password · Gateway</title></svelte:head>

<h1 class="heading">Choose a new password</h1>
<p class="mt-4 text-ink-soft">This signs you out everywhere else.</p>
<form method="POST" class="mt-8 space-y-5">
	<label class="label">
		Reset token
		<input class="field font-mono" name="token" required value={data.token} autocomplete="off" />
	</label>
	<label class="label">
		New password
		<input
			class="field"
			type="password"
			name="password"
			autocomplete="new-password"
			required
			minlength="12"
			maxlength="128"
		/>
	</label>
	<FormError {form} />
	<button class="btn w-full">Change password</button>
</form>
<p class="mt-8"><a class="link" href="/login">Back to sign in</a></p>
