<script>
  import { onMount } from 'svelte';
  import Column from '../lib/components/Column.svelte';
  import RunningCard from '../lib/components/RunningCard.svelte';
  import IssueCard from '../lib/components/IssueCard.svelte';
  import { authedPost, ensureToken } from '../lib/auth.js';
  import { oldestSource, ageSeconds, formatAge } from '../lib/freshness.js';
  import { loadPipeline, pipeline, pipelineError } from '../lib/stores/pipeline.js';
  import { now } from '../lib/stores/now.js';

  const VISIBLE_ENRICH = 10;

  let heldOpen = $state(false);
  let enrichExpanded = $state(false);
  let refreshError = $state(null);
  let dismissed = $state.raw([]);

  onMount(loadPipeline);

  // Dismissed errors are remembered by object identity; every board update
  // brings fresh objects, so a still-failing source shows again.

  const board = $derived($pipeline);
  const loadError = $derived(board ? null : $pipelineError);

  const runningSource = $derived(oldestSource(board, ['runs', 'sessions']));
  const issuesSource = $derived(oldestSource(board, ['issues']));
  const runningLoading = $derived(!board || !runningSource);
  const issuesLoading = $derived(!board || !issuesSource);

  const needsEnrichment = $derived(board?.needsEnrichment ?? []);
  const shownEnrichment = $derived(enrichExpanded ? needsEnrichment : needsEnrichment.slice(0, VISIBLE_ENRICH));
  const hiddenEnrichment = $derived(needsEnrichment.length - shownEnrichment.length);

  const errors = $derived((board?.errors ?? []).filter((e) => !dismissed.includes(e)));

  function runningKey(card, index) {
    return (card.kind === 'session' ? card.sessionId : card.url) || `idx:${index}`;
  }

  function issueKey(card) {
    return `${card.forge}:${card.repo}#${card.number}`;
  }

  function refreshFailure(err) {
    if (err.status === 403) return 'Blocked: wrong origin';
    if (err.status === 503) return 'API token not configured on the server';
    if (err.status === 500) return 'Refresh failed';
    return err.message || 'Refresh failed';
  }

  async function refresh() {
    const token = await ensureToken();
    if (!token) return;
    refreshError = null;
    try {
      await authedPost('/api/pipeline/refresh', {}, token);
      await loadPipeline();
    } catch (err) {
      // a 401 already reopened the token dialog
      if (err.status !== 401) refreshError = refreshFailure(err);
    }
  }

  function errorSince(since) {
    return formatAge(ageSeconds(since, $now));
  }
</script>

<div class="pipeline">
  {#if board?.dispatchPaused}
    <p class="note">⏸ Dispatch paused: ready issues are not started automatically.</p>
  {/if}

  <div class="columns">
    <Column
      title="RUNNING NOW"
      count={board?.running.length ?? null}
      source={runningSource}
      now={$now}
      loading={runningLoading}
      error={loadError}
      emptyText="Nothing running."
      onrefresh={refresh}
      {refreshError}
      onretry={loadPipeline}
    >
      <ul class="cards">
        {#each board.running as card, index (runningKey(card, index))}
          <RunningCard {card} now={$now} />
        {/each}
      </ul>
    </Column>

    <Column
      title="READY TO DISPATCH"
      count={board?.ready.length ?? null}
      source={issuesSource}
      now={$now}
      loading={issuesLoading}
      error={loadError}
      emptyText="Nothing ready."
      onrefresh={refresh}
      {refreshError}
      onretry={loadPipeline}
    >
      <ul class="cards">
        {#each board.ready as card (issueKey(card))}
          <IssueCard {card} action="dispatch" />
        {/each}
      </ul>
      {#snippet extra()}
        {#if board.held.length > 0}
          <div class="held">
            <button type="button" class="collapse" aria-expanded={heldOpen} onclick={() => (heldOpen = !heldOpen)}>
              {heldOpen ? '▾' : '▸'} Held · {board.held.length} <span class="muted">(milestone, open PR, parked)</span>
            </button>
            {#if heldOpen}
              <ul class="cards">
                {#each board.held as card (issueKey(card))}
                  <IssueCard {card} showReason={true} />
                {/each}
              </ul>
            {/if}
          </div>
        {/if}
      {/snippet}
    </Column>

    <Column
      title="NEEDS ENRICHMENT"
      count={board?.needsEnrichment.length ?? null}
      source={issuesSource}
      now={$now}
      loading={issuesLoading}
      error={loadError}
      emptyText="Nothing to enrich."
      startCollapsed={true}
      onrefresh={refresh}
      {refreshError}
      onretry={loadPipeline}
    >
      <ul class="cards">
        {#each shownEnrichment as card (issueKey(card))}
          <IssueCard {card} action="enrich" />
        {/each}
      </ul>
      {#if hiddenEnrichment > 0}
        <button type="button" class="more" onclick={() => (enrichExpanded = true)}>show {hiddenEnrichment} more</button>
      {/if}
    </Column>
  </div>

  {#if errors.length > 0}
    <div class="errors" role="status">
      {#each errors as err (err.source + err.error)}
        <p>
          errors: {err.source} — {err.error} (since {errorSince(err.since)} ago)
          <button type="button" class="link" onclick={() => (dismissed = [...dismissed, err])}>dismiss</button>
        </p>
      {/each}
    </div>
  {/if}
</div>
