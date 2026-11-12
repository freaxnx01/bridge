<script>
  import { onMount } from 'svelte';

  let { repo, number, action, onconfirm, oncancel } = $props();

  let confirmButton;

  const VERBS = { enrich: 'Enrich', dispatch: 'Dispatch', 'clear-lock': 'Clear lock', 'clear-reenrich': 'Clear & re-enrich' };
  const verb = $derived(VERBS[action] ?? 'Dispatch');
  const slot = $derived(
    action === 'clear-lock' ? null : `${action === 'dispatch' ? 'dispatch' : 'enrich'}-${number}`,
  );
  const effect = $derived(
    {
      enrich: `that runs /enrich ${number} --headless: writes the spec and implementation plan and updates the issue body.`,
      dispatch: `that runs /gh:implement ${number}: posts the implementation contract and adds ai-implement, which starts the pipeline.`,
      'clear-reenrich': `that runs /enrich ${number} --headless after removing the enrichment-ongoing label and posting a comment.`,
    }[action],
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
    {#if action === 'clear-lock'}
      <p>This removes the <code>enrichment-ongoing</code> label from the issue and posts a comment. No Claude session is started.</p>
    {:else}
      <p>
        This starts a paid Claude session in worktree <code>{slot}</code> {effect}
      </p>
    {/if}
    <div class="buttons">
      <button type="button" onclick={oncancel}>Cancel</button>
      <button type="button" class="primary" bind:this={confirmButton} onclick={onconfirm}>{verb} ▶</button>
    </div>
  </div>
</div>
