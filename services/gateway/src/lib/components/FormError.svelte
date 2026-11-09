<script lang="ts">
	let { form, on }: { form: { on?: string; message?: string } | null | undefined; on?: string } =
		$props();
</script>

{#if form?.message && form.on === on}
	<p
		class="mt-4 rounded-[4px] border-l-4 border-brick bg-brick/5 px-3 py-2 text-sm text-brick"
		role="alert"
	>
		{form.message}
	</p>
{/if}
