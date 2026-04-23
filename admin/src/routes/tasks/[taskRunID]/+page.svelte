<script lang="ts">
  import { onMount } from "svelte";
  import { page } from "$app/state";
  import TaskShell from "$lib/component/layout/task_shell.svelte";
  import TaskRunDetail from "$lib/component/task/task_run_detail.svelte";
  import TaskResultPanel from "$lib/component/task/task_result_panel.svelte";
  import { loadOwnTaskRun } from "$lib/api/task_api_client";

  let taskDetail: Record<string, unknown> = {};

  onMount(async () => {
    const taskSessionID = page.url.searchParams.get("taskSessionID") ?? "";
    const taskRunID = page.params.taskRunID;
    if (taskSessionID !== "" && taskRunID !== "") {
      taskDetail = await loadOwnTaskRun(taskSessionID, taskRunID);
    }
  });
</script>

<TaskShell>
  <TaskRunDetail taskRun={taskDetail.taskRun as Record<string, unknown> ?? {}} />
  <TaskResultPanel result={String(taskDetail.taskRun?.result ?? "")} />
</TaskShell>
