<script>
  import FreshnessBadge from './FreshnessBadge.svelte';
  import { isStale } from '../freshness.js';

  let {
    title,
    count = null,
    source = null,
    now,
    loading = false,
    error = null,
    emptyText = 'Nothing here.',
    startCollapsed = false,
    onrefresh,
    onretry,
    children,
    extra,
  } = $props();

  // startCollapsed only seeds the initial state; the user toggles it afterwards.
  // svelte-ignore state_referenced_locally
  let collapsed = $state(startCollapsed);

  const stale = $derived(source ? isStale(source.collectedAt, source.intervalSeconds, now) : false);
  const heading = $derived(`${title} · ${loading || count === null ? (error ? '?' : '…') : count}`);
</script>

<section class="column" class:collapsed class:stale>
  <header>
    <button type="button" class="collapse" aria-expanded={!collapsed} onclick={() => (collapsed = !collapsed)}>
      <span class="chevron" aria-hidden="true">{collapsed ? '▸' : '▾'}</span>
      <h2>{heading}</h2>
    </button>
    {#if source}
      <FreshnessBadge collectedAt={source.collectedAt} intervalSeconds={source.intervalSeconds} {now} />
    {/if}
    <button type="button" class="refresh" aria-label="Refresh" title="Refresh the board" onclick={onrefresh}>⟳</button>
  </header>
  <div class="body">
    {#if error}
      <p class="muted">Couldn't load: {error} <button type="button" onclick={onretry}>Retry</button></p>
    {:else if loading}
      <ul class="cards skeleton" aria-busy="true" aria-label="Loading">
        <li class="card"></li><li class="card"></li><li class="card"></li>
      </ul>
    {:else if count === 0}
      <p class="muted">{emptyText}</p>
    {:else}
      {@render children?.()}
    {/if}
    {#if !error && !loading}{@render extra?.()}{/if}
  </div>
</section>
