<script>
  import { authedPost, ensureToken } from '../auth.js';
  import { loadInbox } from '../stores/inbox.js';
  import { shortRef } from '../inbox.js';
  import { ageSeconds, formatAge } from '../freshness.js';
  import ConfirmDialog from './ConfirmDialog.svelte';

  let { card, now, onopen } = $props();

  // One glyph per checks state, so the line reads at a glance on a phone.
  const CHECK_GLYPH = { green: '✓', running: '…', failing: '✗', none: '–' };

  const ev = $derived(card.merge ?? {});
  const age = $derived(formatAge(ageSeconds(card.since, now)));
  const checks = $derived(ev.checks ?? { state: 'none', passed: 0, total: 0 });
  const verdict = $derived(ev.review?.verdict ?? 'none');
  const onOlder = $derived(Boolean(ev.review) && !ev.review.current);
  const files = $derived(ev.files ?? []);
  const additions = $derived(files.reduce((n, f) => n + (f.additions ?? 0), 0));
  const deletions = $derived(files.reduce((n, f) => n + (f.deletions ?? 0), 0));
  const firstLine = $derived((ev.summary ?? '').split('\n').find((l) => l.trim()) ?? '');

  let summaryOpen = $state(false);
  let filesOpen = $state(false);
  // idle | confirm | working | done | failed
  let state = $state('idle');
  let outcome = $state('');
  let failure = $state('');
  let token = null;
  // The evidence the confirm dialog showed, frozen when it opened: the inbox
  // reloads underneath an open dialog, and the merge must post the head the
  // operator confirmed, not whatever the card says by the time they click.
  let pinned = $state(null);

  async function start() {
    token = await ensureToken();
    if (!token) return;
    pinned = $state.snapshot(ev);
    state = 'confirm';
  }

  async function confirm() {
    state = 'working';
    try {
      const body = { repo: card.repo, number: card.number, pr: pinned.pr, headSha: pinned.headSha };
      const res = await authedPost('/api/inbox/merge', body, token);
      outcome = successText(res);
      state = 'done';
      loadInbox();
    } catch (err) {
      failure = failureText(err);
      state = 'failed';
      // A 409 means the card is out of date, so reload it; an auth or origin
      // failure changed nothing on the forge and the dialog says so instead.
      if (err.status !== 401 && err.status !== 403) loadInbox();
    }
  }

  function successText(res) {
    const merged = `Merged PR #${pinned.pr}`;
    if (res.branchDeleted) return merged;
    return `${merged} · branch not deleted: ${res.branchError || 'unknown reason'}`;
  }

  function failureText(err) {
    if (err.status === 401) return 'Token rejected';
    if (err.status === 409) return `Not merged: ${err.message}`;
    if (err.status === 403) return err.message || 'Blocked: wrong origin';
    return err.message || 'request failed';
  }
</script>

<li class="merge-card">
  <button type="button" class="open" onclick={() => onopen(card)}>
    <span class="head">
      <span class="ref"><span class="glyph">✓</span> {shortRef(card)} → PR #{ev.pr}</span>
      <span class="age">{age}</span>
    </span>
    <span class="title">{card.title}</span>
    <span class="meta"
      >checks {CHECK_GLYPH[checks.state] ?? '–'} {checks.passed}/{checks.total} · review: {verdict}{onOlder
        ? ' (older commit)'
        : ''}{ev.stale ? ' · stale' : ''}</span
    >
  </button>

  {#if firstLine}
    <button
      type="button"
      class="summary"
      aria-expanded={summaryOpen}
      onclick={() => (summaryOpen = !summaryOpen)}
    >{summaryOpen ? ev.summary : firstLine}</button>
  {:else}
    <p class="summary empty">no summary in PR body</p>
  {/if}

  {#if files.length > 0}
    <button type="button" class="files" aria-expanded={filesOpen} onclick={() => (filesOpen = !filesOpen)}>
      {filesOpen ? '▾' : '▸'} {files.length} files +{additions} −{deletions}
    </button>
    {#if filesOpen}
      <ul class="file-list">
        {#each files as file (file.path)}
          <li>{file.path} <span class="counts">+{file.additions} −{file.deletions}</span></li>
        {/each}
      </ul>
      {#if ev.filesTruncated}<p class="note">(first 100 shown)</p>{/if}
    {/if}
  {/if}

  <div class="actions">
    <!-- #383 adds the read-only diff next to Open PR. -->
    <a href={ev.prUrl} target="_blank" rel="noopener noreferrer">Open PR ↗</a>
    <!-- Merge is gone once it landed: the result line below is all there is
         left to say, and a second post would only earn the operator a 409 for
         an action that in fact succeeded. -->
    {#if !ev.canMerge}
      <strong class="blocked">{ev.reason || card.reason}</strong>
    {:else if state !== 'done'}
      <button type="button" class="action" disabled={state === 'working'} onclick={start}>
        {state === 'working' ? 'merging…' : 'Merge'}
      </button>
    {/if}
  </div>

  {#if state === 'done'}
    <p class="result ok">✓ {outcome}</p>
  {:else if state === 'failed'}
    <p class="result error" role="alert">
      ✗ {failure}
      <button type="button" class="link" onclick={() => (state = 'idle')}>dismiss</button>
    </p>
  {/if}

  <!-- Inside the card so the list holds only list items; the backdrop is
       position: fixed, so it still covers the page. -->
  {#if state === 'confirm'}
    <ConfirmDialog
      repo={card.repo}
      number={card.number}
      action="merge"
      detail={pinned}
      onconfirm={confirm}
      oncancel={() => (state = 'idle')}
    />
  {/if}
</li>

<style>
  .merge-card {
    list-style: none;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 6px;
    overflow: hidden;
  }
  .open {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    width: 100%;
    min-height: 44px;
    padding: 0.6rem 0.75rem;
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .head {
    display: flex;
    justify-content: space-between;
    gap: 0.5rem;
  }
  .ref {
    font-weight: 600;
  }
  .glyph {
    color: var(--ok);
  }
  .age,
  .meta,
  .note,
  .counts {
    color: var(--muted);
    font-size: 0.85em;
  }
  .title,
  .summary,
  .blocked {
    overflow-wrap: anywhere;
  }
  .summary {
    display: block;
    width: 100%;
    margin: 0;
    padding: 0.25rem 0.75rem;
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    text-align: left;
    white-space: pre-wrap;
    cursor: pointer;
  }
  .summary.empty {
    color: var(--muted);
    cursor: default;
  }
  .files {
    min-height: 44px;
    margin: 0 0.5rem;
    border-color: transparent;
    background: none;
    font-size: 0.9em;
  }
  .file-list {
    margin: 0;
    padding: 0 0.75rem;
  }
  .file-list li {
    list-style: none;
    font-size: 0.9em;
    overflow-wrap: anywhere;
  }
  .note {
    margin: 0;
    padding: 0 0.75rem;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.25rem 1rem;
    padding: 0.25rem 0.75rem 0.5rem;
  }
  .actions a {
    display: inline-flex;
    align-items: center;
    min-height: 44px;
    font-size: 0.9em;
  }
  .action {
    min-height: 44px;
    margin-left: auto;
  }
  .blocked {
    margin-left: auto;
    color: var(--muted);
    font-size: 0.9em;
  }
  .result {
    margin: 0;
    padding: 0 0.75rem 0.5rem;
    font-size: 0.9em;
  }
  .result.error {
    color: var(--error);
  }
  .link {
    min-height: 0;
    padding: 0;
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    text-decoration: underline;
    cursor: pointer;
  }
</style>
