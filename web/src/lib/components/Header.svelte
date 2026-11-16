<script>
  let { area, onnavigate, staleCount = 0, dispatchPaused = false, inboxCount = 0, onToken } = $props();

  const placeholders = ['Repos', 'Issues&PRs', 'Integration', 'Stats', 'Infra'];
</script>

<header class="app-header">
  <span class="brand">◆ bridge cockpit</span>
  <nav aria-label="Areas">
    <button type="button" class="nav" aria-current={area === 'inbox' ? 'page' : undefined} class:active={area === 'inbox'} onclick={() => onnavigate('inbox')}>
      Inbox{#if inboxCount > 0}<span class="badge">{inboxCount}</span>{/if}
    </button>
    <button type="button" class="nav" aria-current={area === 'pipeline' ? 'page' : undefined} class:active={area === 'pipeline'} onclick={() => onnavigate('pipeline')}>Pipeline</button>
    <button type="button" class="nav" aria-current={area === 'dispatcher' ? 'page' : undefined} class:active={area === 'dispatcher'} onclick={() => onnavigate('dispatcher')}>Dispatcher</button>
    {#each placeholders as name (name)}
      <button type="button" class="nav" disabled title="Coming later">{name}</button>
    {/each}
    <button type="button" class="nav" class:active={area === 'arch'} aria-current={area === 'arch' ? 'page' : undefined} onclick={() => onnavigate('arch')}>Arch</button>
  </nav>
  <span class="signals">
    {#if staleCount > 0}<span class="signal warn">⚠ {staleCount} source{staleCount === 1 ? '' : 's'} stale</span>{/if}
    {#if dispatchPaused}<span class="signal">⏸ dispatch paused</span>{/if}
    <button type="button" class="token" aria-label="API token" title="API token" onclick={onToken}>🔑</button>
  </span>
</header>

<style>
  .badge {
    display: inline-block;
    min-width: 1.4em;
    margin-left: 0.4em;
    padding: 0 0.35em;
    border-radius: 999px;
    background: var(--accent);
    color: var(--accent-text);
    font-size: 0.8em;
    font-weight: 700;
    text-align: center;
  }
</style>
