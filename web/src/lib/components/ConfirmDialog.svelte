<script>
  import { onMount } from 'svelte';

  let { repo, number, action, onconfirm, oncancel } = $props();

  let confirmButton;

  const verb = $derived(action === 'enrich' ? 'Enrich' : 'Dispatch');
  const slot = $derived(`${action === 'enrich' ? 'enrich' : 'dispatch'}-${number}`);
  const effect = $derived(
    action === 'enrich'
      ? `that runs /enrich ${number} --headless: writes the spec and implementation plan and updates the issue body.`
      : `that runs /gh:implement ${number}: posts the implementation contract and adds ai-implement, which starts the pipeline.`,
  );

  onMount(() => confirmButton?.focus());

  function onkeydown(event) {
    if (event.key === 'Escape') oncancel();
  }
</script>

<div class="backdrop">
  <div
    class="dialog"
    role="dialog"
    aria-modal="true"
    aria-labelledby="confirm-title"
    tabindex="-1"
    {onkeydown}
  >
    <h2 id="confirm-title">{verb} {repo} #{number}?</h2>
    <p>
      This starts a paid Claude session in worktree <code>{slot}</code> {effect}
    </p>
    <div class="buttons">
      <button type="button" onclick={oncancel}>Cancel</button>
      <button type="button" class="primary" bind:this={confirmButton} onclick={onconfirm}>{verb} ▶</button>
    </div>
  </div>
</div>
