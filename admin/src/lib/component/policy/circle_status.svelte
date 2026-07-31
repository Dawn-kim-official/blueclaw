<script lang="ts">
  type Circle = {
    circleID: string;
    displayName: string;
    mattermostChannelID: string;
    isMattermostManaged: boolean;
    workspaceDirectoryPath: string;
  };

  type MattermostCircleChannel = {
    circleID: string;
    channelName: string;
    channelID: string;
  };

  export let circles: Circle[] = [];
  export let mattermostPrivateChannels: MattermostCircleChannel[] = [];

  function syncChannelForCircle(circleID: string): MattermostCircleChannel | undefined {
    return mattermostPrivateChannels.find(channel => channel.circleID === circleID);
  }
</script>

<section>
  <h2>Circles</h2>
  <ul>
    {#each circles as circle}
      {@const syncChannel = syncChannelForCircle(circle.circleID)}
      <li>
        <strong>{circle.displayName || circle.circleID}</strong>
        · {circle.workspaceDirectoryPath || `/workspace/circles/${circle.circleID}`}
        {#if circle.isMattermostManaged}
          · Managed in Mattermost
          {#if syncChannel?.channelName}
            · {syncChannel.channelName}
          {/if}
        {:else}
          · policy
        {/if}
      </li>
    {/each}
  </ul>
</section>
