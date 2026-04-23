<script lang="ts">
  export let isDisabled = false;
  export let onCancel: () => void = () => {};
</script>

<button disabled={isDisabled} onclick={onCancel} style="padding:0.8rem 1rem;border:none;border-radius:999px;background:#991b1b;color:white">
  Cancel Task
</button>
