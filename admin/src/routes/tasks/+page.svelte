<script lang="ts">
  import { onMount } from "svelte";
  import { page } from "$app/state";
  import TaskShell from "$lib/component/layout/task_shell.svelte";
  import TaskRunTable from "$lib/component/task/task_run_table.svelte";
  import { loadOwnTaskRuns } from "$lib/api/task_api_client";

  let taskRuns: Array<Record<string, unknown>> = [];

  onMount(async () => {
    const taskSessionID = page.url.searchParams.get("taskSessionID") ?? "";
    if (taskSessionID !== "") {
      taskRuns = await loadOwnTaskRuns(taskSessionID);
    }
  });
</script>

<TaskShell>
  <TaskRunTable {taskRuns} />
</TaskShell>
