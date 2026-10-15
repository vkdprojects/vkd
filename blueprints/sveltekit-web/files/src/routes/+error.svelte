<script lang="ts">
	import { ErrorPage } from "@codeatlasdev/ui";
	import { page } from "$app/state";
</script>

<ErrorPage status={page.status} message={page.error?.message} />
