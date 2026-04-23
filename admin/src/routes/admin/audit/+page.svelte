<script lang="ts">
  import { onMount } from "svelte";
  import { loadAuditEntries } from "$lib/api/admin_api_client";
  import AuditLogTable from "$lib/component/audit/audit_log_table.svelte";

  let auditEntries: Array<{ actionName: string; body: string; createdAt: string }> = [];

  onMount(async () => {
    auditEntries = await loadAuditEntries();
  });
</script>

<AuditLogTable {auditEntries} />
