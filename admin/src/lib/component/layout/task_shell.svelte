<script lang="ts">
  let { children } = $props();
</script>

<div style="min-height:100vh;background:#fafaf9;color:#111827;font-family:Georgia,serif">
  <header style="padding:1.25rem 1.5rem;border-bottom:1px solid #e7e5e4">
    <h1 style="margin:0;font-size:1.6rem">Blueclaw Tasks</h1>
  </header>
  <main style="padding:1.5rem;max-width:960px;margin:0 auto">{@render children?.()}</main>
</div>
