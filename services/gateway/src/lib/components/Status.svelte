<script lang="ts">
	type Tone = 'good' | 'warn' | 'bad' | 'quiet';
	let { tone, children }: { tone: Tone; children: import('svelte').Snippet } = $props();

	const tones: Record<Tone, string> = {
		good: 'text-moss before:bg-moss',
		warn: 'text-amber before:bg-amber',
		bad: 'text-brick before:bg-brick',
		quiet: 'text-ink-soft before:bg-line'
	};
</script>

<span
	class="inline-flex items-center gap-1.5 text-sm font-semibold before:size-2 before:rounded-full {tones[
		tone
	]}"
>
	{@render children()}
</span>
