<script>
  let { area, onnavigate, staleCount = 0, dispatchPaused = false, onToken } = $props();

  const placeholders = ['Repos', 'Issues&PRs', 'Integration', 'Stats', 'Infra'];
</script>

<header class="app-header">
  <span class="brand">◆ bridge cockpit</span>
  <nav aria-label="Areas">
    <button type="button" class="nav" aria-current={area === 'pipeline' ? 'page' : undefined} class:active={area === 'pipeline'} onclick={() => onnavigate('pipeline')}>Pipeline</button>
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
