<script lang="ts">
  let { children } = $props();
</script>

<div style="min-height:100vh;background:#f5f4ef;color:#1f2937;font-family:Georgia,serif">
  <header style="padding:1.5rem 2rem;border-bottom:1px solid #d6d3d1">
    <h1 style="margin:0;font-size:1.8rem">Blueclaw Admin</h1>
  </header>
  <main style="padding:2rem;max-width:1100px;margin:0 auto">{@render children?.()}</main>
</div>
