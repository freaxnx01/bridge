<script>
  // GateStrip shows every bound the dispatcher is under right now: the window,
  // the budget rung, the nightly and global caps, the pause flag, the last
  // tick and each lane's mode. Same data as `bridge dispatch status`.
  let { gates } = $props();

  // hhmm is a plain 24h clock reading in the operator's own timezone. The
  // cockpit is a single-locale operator console (see the English column
  // headings), so it formats units rather than localizing them.
  function hhmm(iso) {
    const d = new Date(iso);
    return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  }

  const windowText = $derived.by(() => {
    if (gates.windowOpen) return 'window open';
    if (gates.nextOpening) return `window closed · opens ${hhmm(gates.nextOpening)}`;
    return 'window closed';
  });

  // The rung is a percentage of the allowance, which is the number the
  // operator acts on: 4357% says "stop looking at the board" at a glance.
  const budgetPct = $derived(
    gates.allowedUsd > 0 ? Math.round((gates.usedUsd / gates.allowedUsd) * 100) : 0,
  );
  const budgetText = $derived.by(() => {
    if (!gates.rungOn) return 'budget rung off';
    if (!gates.budgetKnown) return 'budget unreadable';
    return `budget ${budgetPct}%`;
  });
  const budgetWarn = $derived(gates.rungOn && (!gates.budgetKnown || budgetPct >= 100));

  function laneText(lane) {
    return `${lane.name}: ${lane.dryRun ? 'dry-run' : 'live'} ${lane.dispatched}/${lane.max > 0 ? lane.max : '∞'}`;
  }
</script>

{#if gates}
  <div class="gates" role="status" aria-label="Dispatcher gates">
    <span class="gate" class:warn={!gates.windowOpen}>{windowText}</span>
    <span class="gate" class:warn={budgetWarn}>{budgetText}</span>
    <span class="gate">night {gates.nightCount}/{gates.nightCap}</span>
    <span class="gate">PRs {gates.globalOpen}/{gates.globalCap}</span>
    {#if gates.paused}<span class="gate warn">paused</span>{/if}
    {#if gates.lastTick}<span class="gate muted">last tick {hhmm(gates.lastTick)}</span>{/if}
    {#each gates.lanes ?? [] as lane (lane.name)}
      <span class="gate">{laneText(lane)}</span>
    {/each}
  </div>
{/if}

<style>
  .gates {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem 0.6rem;
    margin: 0 0 0.6rem;
    font-size: 0.85em;
  }
  .gate {
    padding: 0.1em 0.5em;
    border: 1px solid var(--border);
    border-radius: 999px;
    white-space: nowrap;
  }
  .warn {
    border-color: var(--warn, #b58900);
    color: var(--warn, #b58900);
  }
  .muted {
    opacity: 0.7;
  }
</style>
