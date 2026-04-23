<script lang="ts">
  export let auditEntries: Array<{ actionName: string; body: string; createdAt: string }> = [];
</script>

<section>
  <h2>Audit</h2>
  <ul>
    {#each auditEntries as auditEntry}
      <li>{auditEntry.actionName} · {auditEntry.body} · {auditEntry.createdAt}</li>
    {/each}
  </ul>
</section>
