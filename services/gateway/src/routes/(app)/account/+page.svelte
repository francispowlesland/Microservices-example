<script lang="ts">
	import { enhance } from '$app/forms';
	import FormError from '#lib/components/FormError.svelte';
	import PageHead from '#lib/components/PageHead.svelte';
	import ProfileForm from '#lib/components/ProfileForm.svelte';
	import { formatTime } from '#lib/format.js';

	let { data, form } = $props();
</script>

<PageHead title="Your account">
	{data.user.email} · member since {formatTime(data.user.createdAt)}
</PageHead>

<div class="grid max-w-4xl gap-12 md:grid-cols-2">
	<section>
		<h2 class="subheading mb-4">Profile</h2>
		<ProfileForm user={data.user} {form} />
		<p class="mt-4 text-sm text-ink-soft">
			Your email can’t be changed here. Last signed in {formatTime(data.user.lastLoginAt)}.
		</p>
	</section>

	<section>
		<h2 class="subheading mb-4">Password</h2>
		<form method="POST" action="?/password" class="space-y-4" use:enhance>
			<label class="label">
				Current password
				<input
					class="field"
					type="password"
					name="current"
					autocomplete="current-password"
					required
				/>
			</label>
			<label class="label">
				New password
				<input
					class="field"
					type="password"
					name="new"
					autocomplete="new-password"
					required
					minlength="12"
					maxlength="128"
				/>
			</label>
			<FormError {form} on="password" />
			<button class="btn-quiet">Change password</button>
			<p class="text-sm text-ink-soft">You’ll be signed out everywhere, including here.</p>
		</form>
	</section>

	<section class="border-t border-line pt-6 md:col-span-2">
		<h2 class="subheading text-brick">Delete account</h2>
		<p class="mt-1 max-w-prose text-ink-soft">
			This removes your account and signs you out. Your orders and payments stay on record.
		</p>
		<form
			method="POST"
			action="?/delete"
			class="mt-4"
			use:enhance={({ cancel }) => {
				if (!confirm('Delete your account permanently?')) cancel();
			}}
		>
			<FormError {form} on="delete" />
			<button class="btn-danger">Delete my account</button>
		</form>
	</section>
</div>
