<script lang="ts">
  import { onMount } from "svelte";
  import { loadPolicyDocument } from "$lib/api/admin_api_client";
  import PeopleEditor from "$lib/component/policy/people_editor.svelte";
  import ChannelRuleEditor from "$lib/component/policy/channel_rule_editor.svelte";
  import RetentionEditor from "$lib/component/policy/retention_editor.svelte";
  import ValidationPanel from "$lib/component/policy/validation_panel.svelte";

  let policyDocument = {
    people: [],
    channels: [],
    retention: {
      rawEventDays: 60
    }
  };

  onMount(async () => {
    policyDocument = await loadPolicyDocument();
  });
</script>

<PeopleEditor people={policyDocument.people} />
<ChannelRuleEditor channels={policyDocument.channels} />
<RetentionEditor rawEventDays={policyDocument.retention.rawEventDays} />
<ValidationPanel message="Policy file is loaded from the backend." />
