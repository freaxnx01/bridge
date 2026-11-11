<script>
  import { shortRef, prUrl } from '../inbox.js';
  import { ageSeconds, formatAge } from '../freshness.js';
  import { loadIssueDetail } from '../stores/inbox.js';
  import { now } from '../stores/now.js';

  let { repo, number, onclose } = $props();

  let detail = $state(null);
  let failed = $state(false);

  const ref = $derived(shortRef({ repo, number }));
  const labels = $derived((detail?.issue?.labels ?? []).join(', '));

  // The panel stays mounted while another card is opened, so the load follows
  // the props: an in-flight load of the issue just left must not land on the
  // one now shown, and its failure must not stick to it either.
  $effect(() => {
    const wanted = { repo, number };
    detail = null;
    failed = false;
    let current = true;
    loadIssueDetail(wanted.repo, wanted.number).then(
      (loaded) => {
        if (current) detail = loaded;
      },
      () => {
        if (current) failed = true;
      },
    );
    return () => {
      current = false;
    };
  });

  function onKeydown(event) {
    if (event.key === 'Escape') onclose();
  }

  function cost(run) {
    return `$${(run.costUsd ?? 0).toFixed(2)}`;
  }

  function runKey(run, index) {
    return run.runUrl || `${run.posted}:${index}`;
  }
</script>

<svelte:window on:keydown={onKeydown} />

<aside class="detail" role="dialog" aria-label={ref}>
  <header>
    <button type="button" class="back" aria-label="Back" onclick={onclose}>←</button>
    <span class="ref">{ref}</span>
    <span class="title">{detail?.issue?.title ?? ''}</span>
  </header>
  {#if labels}<p class="labels">labels: {labels}</p>{/if}

  {#if failed}
    <p class="error" role="alert">Could not load issue.</p>
  {:else if !detail}
    <p class="muted">Loading…</p>
  {:else if detail.runs.length === 0}
    <p class="muted">No run reports.</p>
  {:else}
    <h3>Runs (newest first)</h3>
    <ul class="runs">
      {#each detail.runs as run, index (runKey(run, index))}
        <li class="run">
          <div class="line1">
            {#if run.attempt > 0}<strong>#{run.attempt}</strong>{/if}
            <span>{run.class}</span>
            <span>{run.decision}</span>
            <span>{cost(run)}</span>
          </div>
          <div class="line2">{run.agent} · {run.model} · {run.turns} t</div>
          <div class="line3">
            {#if run.pr > 0}
              <a href={prUrl(repo, run.pr)} target="_blank" rel="noopener noreferrer">PR #{run.pr} ↗</a>
            {/if}
            {#if run.runUrl}
              <a href={run.runUrl} target="_blank" rel="noopener noreferrer">run ↗</a>
            {/if}
            <span class="age">{formatAge(ageSeconds(run.posted, $now))}</span>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</aside>

<style>
  .detail {
    position: fixed;
    inset: 0;
    z-index: 20;
    overflow-y: auto;
    padding: 1rem;
    background: var(--surface);
    border-left: 1px solid var(--border);
  }
  header {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .back {
    min-width: 44px;
    min-height: 44px;
    border-color: transparent;
    background: none;
    font-size: 1.1rem;
  }
  .ref {
    font-weight: 700;
  }
  .title,
  .labels {
    overflow-wrap: anywhere;
  }
  .labels,
  .muted,
  .age {
    color: var(--muted);
  }
  .error {
    color: var(--error);
  }
  h3 {
    margin: 1rem 0 0.5rem;
    font-size: 0.95rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--muted);
  }
  .runs {
    margin: 0;
    padding: 0;
  }
  .run {
    list-style: none;
    padding: 0.5rem 0;
    border-bottom: 1px solid var(--border);
  }
  .line1,
  .line3 {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 0.75rem;
  }
  .line2 {
    color: var(--muted);
    font-size: 0.9em;
  }
  .line3 a {
    display: inline-flex;
    align-items: center;
    min-height: 44px;
  }
  @media (min-width: 900px) {
    .detail {
      left: auto;
      width: min(480px, 100vw);
    }
  }
</style>
