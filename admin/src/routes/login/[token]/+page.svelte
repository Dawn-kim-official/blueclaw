<script lang="ts">
  import TaskShell from "$lib/component/layout/task_shell.svelte";
  import { page } from "$app/state";
</script>

<TaskShell>
  <p>Magic link token: {page.params.token}</p>
</TaskShell>
