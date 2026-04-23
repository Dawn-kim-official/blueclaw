<script lang="ts">
  export let channels: Array<{ displayName: string; platform: string; defaultSecurityLevelRank: number }> = [];
</script>

<section>
  <h2>Channels</h2>
  <ul>
    {#each channels as channel}
      <li>{channel.platform} · {channel.displayName} · level {channel.defaultSecurityLevelRank}</li>
    {/each}
  </ul>
</section>
