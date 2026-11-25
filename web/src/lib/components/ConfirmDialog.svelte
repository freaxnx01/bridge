<script>
  import { onMount } from 'svelte';

  // detail carries the merge evidence of a merge confirmation, and is unset
  // for every other action.
  let { repo, number, action, detail, onconfirm, oncancel } = $props();

  let confirmButton;

  const VERBS = { enrich: 'Enrich', dispatch: 'Dispatch', 'clear-lock': 'Clear lock', 'clear-reenrich': 'Clear & re-enrich', merge: 'Squash & merge' };
  const verb = $derived(VERBS[action] ?? 'Dispatch');
  // The merge title names the PR and its base branch, because that — not the
  // issue — is what the click changes.
  const title = $derived(
    action === 'merge' ? `Merge PR #${detail?.pr} into ${detail?.baseRef}?` : `${verb} ${repo} #${number}?`,
  );
  const mergeEffect = $derived(mergeSentence());
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

  // mergeSentence spells out exactly what the merge does: which commit lands,
  // on what evidence, and that the branch goes with it.
  function mergeSentence() {
    const checks = detail?.checks ?? { passed: 0, total: 0 };
    const sha = (detail?.headSha ?? '').slice(0, 7);
    const verdictPart = detail?.review ? `review ${detail.review.verdict} on ${sha} (head)` : 'no review';
    return `${repo} · squash · checks ${checks.passed}/${checks.total} ✓ · ${verdictPart} · Closes #${number}. The branch is deleted after merge.`;
  }

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
    <h2 id="confirm-title">{title}</h2>
    {#if action === 'merge'}
      <p>{mergeEffect}</p>
    {:else if action === 'clear-lock'}
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
