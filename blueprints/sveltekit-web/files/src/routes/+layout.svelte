<script lang="ts">
	import "../app.css";
	import { colorScheme, Toaster } from "@codeatlasdev/ui";
	import { onMount } from "svelte";

	const { children } = $props();

	onMount(() => colorScheme.init());
</script>

{@render children()}

<Toaster />
