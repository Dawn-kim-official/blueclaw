<script lang="ts">
  import AdminShell from "$lib/component/layout/admin_shell.svelte";

  let { children } = $props();
</script>

<AdminShell>
  {@render children?.()}
</AdminShell>
