<script lang="ts">
	import { Button } from "@codeatlasdev/ui";

	let { start = 0 }: { start?: number } = $props();
	// svelte-ignore state_referenced_locally
	let count = $state(start);
</script>

<Button variant="filled" onclick={() => count++}>Clicked {count} {count === 1 ? "time" : "times"}</Button>
