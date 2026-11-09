<script lang="ts">
	import { enhance } from '$app/forms';
	import FormError from '#lib/components/FormError.svelte';
	import PageHead from '#lib/components/PageHead.svelte';
	import ProfileForm from '#lib/components/ProfileForm.svelte';
	import Status from '#lib/components/Status.svelte';
	import { formatCents, formatTime, shortId } from '#lib/format.js';
	import { userStatus } from '#lib/labels.js';

	let { data, form } = $props();
	const status = $derived(userStatus[data.user.status]);
</script>

<PageHead title={data.user.name}>
	@{data.user.username} · {data.user.email}
</PageHead>

<div class="grid gap-12 lg:grid-cols-[minmax(0,1fr)_22rem]">
	<div>
		<dl class="grid grid-cols-2 gap-6 sm:grid-cols-4">
			<div>
				<dt class="text-sm text-ink-soft">Status</dt>
				<dd class="mt-1"><Status tone={status[1]}>{status[0]}</Status></dd>
			</div>
			<div>
				<dt class="text-sm text-ink-soft">Roles</dt>
				<dd class="mt-1 capitalize">{data.user.roles.join(', ')}</dd>
			</div>
			<div>
				<dt class="text-sm text-ink-soft">Joined</dt>
				<dd class="mt-1">{formatTime(data.user.createdAt)}</dd>
			</div>
			<div>
				<dt class="text-sm text-ink-soft">Last sign-in</dt>
				<dd class="mt-1">{formatTime(data.user.lastLoginAt)}</dd>
			</div>
		</dl>

		<h2 class="subheading mt-12 border-b border-ink pb-2">Recent orders</h2>
		{#if data.orders.length === 0}
			<p class="mt-4 text-ink-soft">No orders.</p>
		{:else}
			<ul class="divide-y divide-line">
				{#each data.orders as o (o.id)}
					<li>
						<a href="/orders/{o.id}" class="flex justify-between py-2.5 hover:text-signal">
							<span class="font-mono text-sm">{shortId(o.id)}</span>
							<span class="font-semibold">{formatCents(o.totalCents)}</span>
						</a>
					</li>
				{/each}
			</ul>
			<a class="link mt-3 inline-block text-sm" href="/orders?user={data.user.id}"
				>All their orders</a
			>
		{/if}
	</div>

	<aside class="space-y-6">
		<section class="panel p-5">
			<h2 class="subheading mb-4">Profile</h2>
			<ProfileForm user={data.user} {form} />
		</section>
		<section class="panel p-5">
			<h2 class="subheading text-brick">Delete user</h2>
			<p class="mt-1 text-sm text-ink-soft">Removes the account and signs them out everywhere.</p>
			<form
				method="POST"
				action="?/delete"
				class="mt-4"
				use:enhance={({ cancel }) => {
					if (!confirm(`Delete ${data.user.name}'s account permanently?`)) cancel();
				}}
			>
				<FormError {form} on="delete" />
				<button class="btn-danger w-full">Delete user</button>
			</form>
		</section>
	</aside>
</div>
