<script lang="ts">
  export let taskRuns: Array<Record<string, unknown>> = [];
</script>

<table style="width:100%;border-collapse:collapse;background:white">
  <thead>
    <tr>
      <th style="text-align:left;padding:0.75rem;border-bottom:1px solid #e7e5e4">Task</th>
      <th style="text-align:left;padding:0.75rem;border-bottom:1px solid #e7e5e4">Status</th>
    </tr>
  </thead>
  <tbody>
    {#each taskRuns as taskRun}
      <tr>
        <td style="padding:0.75rem;border-bottom:1px solid #f5f5f4">{String(taskRun.taskRunID ?? "")}</td>
        <td style="padding:0.75rem;border-bottom:1px solid #f5f5f4">{String(taskRun.status ?? "")}</td>
      </tr>
    {/each}
  </tbody>
</table>
