<script lang="ts">
  export let people: Array<{ personID: string; displayName: string; emails: string[] }> = [];
</script>

<section>
  <h2>People</h2>
  <ul>
    {#each people as person}
      <li>{person.displayName} · {person.emails.join(", ")}</li>
    {/each}
  </ul>
</section>
