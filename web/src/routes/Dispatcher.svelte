<script>
  import { onMount } from 'svelte';
  import GateStrip from '../lib/components/GateStrip.svelte';
  import { loadHistory, loadTick } from '../lib/stores/dispatchHistory.js';

  const PERIODS = [1, 7, 30];

  let days = $state(7);
  let history = $state(null);
  let loadError = $state(null);
  let selectedIssue = $state(null); // { repo, issue } | null
  let tick = $state(null);
  let tickError = $state(null);

  onMount(() => refresh());

  async function refresh() {
    loadError = null;
    try {
      history = await loadHistory(days);
    } catch (err) {
      loadError = err.message || 'could not read the dispatch history';
    }
  }

  async function pickPeriod(next) {
    days = next;
    selectedIssue = null;
    tick = null;
    await refresh();
  }

  const summary = $derived(history?.summary ?? []);
  // Newest tick first: "what did it just do" is the question asked most.
  const ticks = $derived([...(history?.ticks ?? [])].reverse());
  const shownTicks = $derived(
    selectedIssue ? ticks.filter((t) => covers(t, selectedIssue)) : ticks,
  );
  const empty = $derived(Boolean(history) && summary.length === 0 && ticks.length === 0);

  function covers(t, ref) {
    return (t.candidates ?? []).some((c) => c.repo === ref.repo && c.issue === ref.issue);
  }

  // Selecting an issue narrows the timeline to the ticks that looked at it and
  // opens the most recent one: the summary row's "typical why" is an average,
  // and the operator's next question is always a concrete tick.
  async function selectIssue(row) {
    const ref = { repo: row.repo, issue: row.issue };
    selectedIssue = ref;
    const latest = ticks.find((t) => covers(t, ref));
    if (latest) await openTick(latest);
  }

  async function openTick(header) {
    tickError = null;
    try {
      tick = await loadTick(header.at);
    } catch (err) {
      tick = null;
      tickError = err.message || 'could not read that tick';
    }
  }

  function hhmm(iso) {
    const d = new Date(iso);
    return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  }

  function dayLabel(iso) {
    const d = new Date(iso);
    return `${String(d.getDate()).padStart(2, '0')}.${String(d.getMonth() + 1).padStart(2, '0')}`;
  }

  // A decision the dispatcher chose: a dry-run lane's would_dispatch counts,
  // and so does a live one it meant to apply and could not — both are picks.
  const PICKED = ['dispatched', 'would_dispatch', 'not_applied'];
  const isPick = (d) => PICKED.includes(d.decision);

  function why(d) {
    const head = isPick(d) ? d.rank_reason : d.reason;
    return [head, d.lane_reason].filter(Boolean).join(' · ');
  }
</script>

<section class="dispatcher">
  <div class="controls">
    <span class="label">Period</span>
    {#each PERIODS as p (p)}
      <button type="button" class="period" class:active={days === p} onclick={() => pickPeriod(p)}>{p}d</button>
    {/each}
    {#if selectedIssue}
      <button type="button" class="link" onclick={() => (selectedIssue = null)}>
        clear filter ({selectedIssue.repo} #{selectedIssue.issue})
      </button>
    {/if}
  </div>

  {#if loadError}
    <p class="error" role="alert">{loadError}</p>
  {:else if empty}
    <p class="muted">No dispatch ticks recorded yet.</p>
  {:else if history}
    <table class="summary">
      <caption>Per issue · timer ticks only</caption>
      <thead>
        <tr><th>Issue</th><th>Picked</th><th>Typical why</th></tr>
      </thead>
      <tbody>
        {#each summary as row (row.repo + '#' + row.issue)}
          <tr
            class:selected={selectedIssue?.repo === row.repo && selectedIssue?.issue === row.issue}
            onclick={() => selectIssue(row)}
          >
            <td><strong>{row.repo}</strong> <button type="button" class="link">#{row.issue}</button> {row.title}</td>
            <td class="num">{row.pickedTicks} / {row.evaluatedTicks}</td>
            <td class="reason">{row.topReason}</td>
          </tr>
        {/each}
      </tbody>
    </table>

    <div class="timeline">
      {#each shownTicks as t (t.at)}
        <button
          type="button"
          class="chip"
          class:idle={t.pickedCount === 0}
          class:active={tick?.at === t.at}
          onclick={() => openTick(t)}
        >
          <span class="when">{dayLabel(t.at)} {hhmm(t.at)}</span>
          <span class="count">{t.pickedCount}/{t.pickedCount + t.skippedCount}</span>
          {#if t.trigger === 'manual'}<span class="marker">manual</span>{/if}
          {#if t.outcome !== 'decided'}<span class="marker">{t.outcome}</span>{/if}
        </button>
      {/each}
    </div>

    {#if tickError}
      <p class="error" role="alert">{tickError}</p>
    {:else if tick}
      <div class="detail">
        <h3>{dayLabel(tick.at)} {hhmm(tick.at)} · {tick.trigger} · {tick.mode}</h3>
        <GateStrip gates={tick.gates} />
        <table class="decisions">
          <thead>
            <tr><th>#</th><th>Issue</th><th>Verdict</th><th>Why</th></tr>
          </thead>
          <tbody>
            {#each tick.decisions ?? [] as d (d.repo + '#' + d.issue)}
              <tr>
                <td class="num">{d.rank || ''}</td>
                <td><strong>{d.repo}</strong> #{d.issue} {d.title}</td>
                <td class={isPick(d) ? 'pick' : 'skip'}>{isPick(d) ? '✓ would pick' : '✗ skip'}</td>
                <td class="reason">{why(d)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</section>

<style>
  .dispatcher {
    padding: 0 1rem 2rem;
  }
  .controls {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin: 0.6rem 0;
  }
  .label {
    opacity: 0.7;
  }
  .period.active {
    font-weight: 700;
    text-decoration: underline;
  }
  table {
    width: 100%;
    border-collapse: collapse;
  }
  caption {
    text-align: left;
    opacity: 0.7;
    padding: 0.3rem 0;
  }
  th,
  td {
    text-align: left;
    padding: 0.25rem 0.5rem;
    border-bottom: 1px solid var(--border);
    vertical-align: top;
  }
  .num {
    text-align: right;
    white-space: nowrap;
  }
  .summary tbody tr:hover {
    background: var(--hover, rgba(127, 127, 127, 0.1));
  }
  .summary tbody tr.selected {
    outline: 1px solid var(--accent);
  }
  .timeline {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
    margin: 0.8rem 0;
  }
  .chip {
    display: inline-flex;
    gap: 0.4em;
    padding: 0.15em 0.5em;
    border: 1px solid var(--border);
    border-radius: 0.4em;
    background: none;
    color: inherit;
    font: inherit;
    font-size: 0.85em;
    cursor: pointer;
  }
  .chip.idle {
    opacity: 0.55;
  }
  .chip.active {
    outline: 1px solid var(--accent);
  }
  .marker {
    opacity: 0.7;
    font-style: italic;
  }
  .pick {
    color: var(--ok, #2aa198);
  }
  .reason {
    opacity: 0.85;
  }
  .muted {
    opacity: 0.7;
  }
</style>
