<script lang="ts">
	import "../app.css";
	import { colorScheme, Toaster } from "@codeatlasdev/ui";
	import { nativeFeel } from "@codeatlasdev/ui/kit";
	import { onMount, type Snippet } from "svelte";

	const { children }: { children: Snippet } = $props();

	onMount(() => {
		colorScheme.init();
		// Focus rings only on keyboard navigation, no web context menu, no page zoom.
		return nativeFeel({ blockZoom: true });
	});
</script>

{@render children()}

<Toaster />
