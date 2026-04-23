<script lang="ts">
  export let taskEvents: Array<Record<string, unknown>> = [];
</script>

<section>
  <h2>Task Events</h2>
  <ul>
    {#each taskEvents as taskEvent}
      <li>{String(taskEvent.name ?? "")} · {String(taskEvent.body ?? "")}</li>
    {/each}
  </ul>
</section>
