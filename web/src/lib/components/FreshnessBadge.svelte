<script>
  import { ageSeconds, formatAge, isStale } from '../freshness.js';

  let { collectedAt, intervalSeconds, now } = $props();

  const stale = $derived(isStale(collectedAt, intervalSeconds, now));
  const age = $derived(formatAge(ageSeconds(collectedAt, now)));
</script>

<span class="fresh" class:stale data-testid="freshness" title={stale ? 'Data is older than twice its refresh interval' : 'Data age'}>
  {#if stale}⚠ {/if}{age}
</span>

<style>
  .fresh { color: var(--muted); font-variant-numeric: tabular-nums; }
  .fresh.stale { color: var(--warn); font-weight: 600; }
</style>
