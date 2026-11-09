<script lang="ts">
	import { enhance } from '$app/forms';
	import FormError from './FormError.svelte';
	import type { User } from '#lib/gen/user/v1/user_pb.js';

	let {
		user,
		form
	}: { user: User; form: { on?: string; message?: string; saved?: boolean } | null } = $props();
</script>

<form
	method="POST"
	action="?/profile"
	class="space-y-4"
	use:enhance={() =>
		({ update }) =>
			update({ reset: false })}
>
	<input type="hidden" name="was_name" value={user.name} />
	<input type="hidden" name="was_username" value={user.username} />
	<label class="label">
		Full name
		<input class="field" name="name" required maxlength="100" value={user.name} />
	</label>
	<label class="label">
		Username
		<input
			class="field"
			name="username"
			required
			minlength="3"
			maxlength="32"
			pattern="[a-zA-Z0-9_]+"
			value={user.username}
		/>
	</label>
	<FormError {form} on="profile" />
	{#if form?.on === 'profile' && form.saved}
		<p class="text-sm text-moss" role="status">Profile saved.</p>
	{/if}
	<button class="btn-quiet">Save profile</button>
</form>
