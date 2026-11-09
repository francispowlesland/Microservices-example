<script lang="ts">
	import PageHead from '#lib/components/PageHead.svelte';
	import Pager from '#lib/components/Pager.svelte';
	import Status from '#lib/components/Status.svelte';
	import { formatTime } from '#lib/format.js';
	import { userStatus } from '#lib/labels.js';

	let { data } = $props();
</script>

<PageHead title="Users"
	>Everyone with an account. Roles and suspensions are managed in the database.</PageHead
>

<table class="w-full text-left">
	<thead class="border-b border-ink text-sm text-ink-soft">
		<tr>
			<th class="py-2 font-medium">Name</th>
			<th class="hidden py-2 font-medium md:table-cell">Email</th>
			<th class="py-2 font-medium">Status</th>
			<th class="hidden py-2 font-medium lg:table-cell">Last sign-in</th>
		</tr>
	</thead>
	<tbody class="divide-y divide-line">
		{#each data.users as u (u.id)}
			{@const [label, tone] = userStatus[u.status]}
			<tr class="hover:bg-paper">
				<td class="py-2.5">
					<a class="link" href="/users/{u.id}">{u.name}</a>
					<span class="text-sm text-ink-soft">@{u.username}</span>
					{#if u.roles.includes('admin')}
						<span
							class="ml-1 rounded-full border border-signal px-1.5 text-xs font-semibold text-signal"
							>Admin</span
						>
					{/if}
				</td>
				<td class="hidden py-2.5 md:table-cell">{u.email}</td>
				<td class="py-2.5"><Status {tone}>{label}</Status></td>
				<td class="hidden py-2.5 text-ink-soft lg:table-cell">{formatTime(u.lastLoginAt)}</td>
			</tr>
		{/each}
	</tbody>
</table>
<Pager next={data.next} />
