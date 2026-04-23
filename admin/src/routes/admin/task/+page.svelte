<script lang="ts">
  import { onMount } from "svelte";
  import { loadAdminTaskRuns } from "$lib/api/admin_api_client";
  import TaskRunTable from "$lib/component/task/task_run_table.svelte";

  let taskRuns: Array<Record<string, unknown>> = [];

  onMount(async () => {
    taskRuns = await loadAdminTaskRuns();
  });
</script>

<TaskRunTable {taskRuns} />
