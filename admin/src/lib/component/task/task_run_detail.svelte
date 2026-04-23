<script lang="ts">
  export let taskRun: Record<string, unknown> = {};
</script>

<section style="padding:1rem;border:1px solid #d6d3d1;border-radius:0.75rem;background:white">
  <h2>Task Detail</h2>
  <pre>{JSON.stringify(taskRun, null, 2)}</pre>
</section>
