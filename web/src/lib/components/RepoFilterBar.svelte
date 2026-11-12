<script>
  import {
    repoKey, isFiltered, setForge, setOwner, toggleRepo, setRepos, clearFilter,
  } from '../stores/repoFilter.js';

  let { repos = [], filter, counts = {} } = $props();

  // Forges in the operator's reading order; anything else follows alphabetically.
  const FORGE_ORDER = ['github', 'forgejo', 'ado'];
  const FORGE_LABELS = { github: 'GitHub', forgejo: 'Forgejo', ado: 'ADO' };

  let open = $state(false);
  let search = $state('');
  let bar;

  const forges = $derived(countBy(repos, (r) => r.forge).sort(byForgeOrder));
  const owners = $derived(
    filter.forge
      ? countBy(repos.filter((r) => r.forge === filter.forge), (r) => r.owner).sort(byNameInsensitive)
      : [],
  );

  // The dropdown shows what the chips and the search box leave.
  const listed = $derived(
    repos.filter(
      (r) =>
        (!filter.forge || r.forge === filter.forge) &&
        (!filter.owner || r.owner === filter.owner) &&
        `${r.owner}/${r.name}`.toLowerCase().includes(search.trim().toLowerCase()),
    ),
  );
  const groups = $derived(groupByForgeThenOwner(listed));

  const buttonLabel = $derived(labelFor(filter.repos));

  function labelFor(keys) {
    if (keys.length === 0) return 'All repos';
    if (keys.length === 1) return keys[0].slice(keys[0].lastIndexOf('/') + 1);
    return `${keys.length} repos`;
  }

  function forgeLabel(forge) {
    return FORGE_LABELS[forge] ?? forge;
  }

  function countBy(items, key) {
    const counted = new Map();
    for (const item of items) counted.set(key(item), (counted.get(key(item)) ?? 0) + 1);
    return [...counted].map(([name, count]) => ({ name, count }));
  }

  function byForgeOrder(a, b) {
    const ai = FORGE_ORDER.indexOf(a.name);
    const bi = FORGE_ORDER.indexOf(b.name);
    if (ai !== bi) return (ai < 0 ? FORGE_ORDER.length : ai) - (bi < 0 ? FORGE_ORDER.length : bi);
    return byNameInsensitive(a, b);
  }

  function byNameInsensitive(a, b) {
    return a.name.toLowerCase().localeCompare(b.name.toLowerCase());
  }

  function groupByForgeThenOwner(list) {
    const forgeNames = countBy(list, (r) => r.forge).sort(byForgeOrder);
    return forgeNames.map(({ name: forge }) => {
      const mine = list.filter((r) => r.forge === forge);
      const ownerNames = countBy(mine, (r) => r.owner).sort(byNameInsensitive);
      return {
        forge,
        owners: ownerNames.map(({ name: owner }) => ({
          owner,
          repos: mine
            .filter((r) => r.owner === owner)
            .map((r) => ({ name: r.name, label: `${owner}/${r.name}`, key: repoKey(r) }))
            .sort(byNameInsensitive),
        })),
      };
    });
  }

  function allSelected(ownerGroup) {
    return ownerGroup.repos.every((r) => filter.repos.includes(r.key));
  }

  function toggleOwner(ownerGroup) {
    const keys = ownerGroup.repos.map((r) => r.key);
    const rest = filter.repos.filter((key) => !keys.includes(key));
    setRepos(allSelected(ownerGroup) ? rest : [...rest, ...keys]);
  }

  function close() {
    open = false;
    search = '';
  }

  function onWindowKeydown(event) {
    if (open && event.key === 'Escape') close();
  }

  function onWindowClick(event) {
    if (open && bar && !bar.contains(event.target)) close();
  }
</script>

<svelte:window onkeydown={onWindowKeydown} onclick={onWindowClick} />

<div class="repo-filter" bind:this={bar}>
  <div class="chips" role="group" aria-label="Forge">
    <span class="chips-label">Forge:</span>
    <button type="button" class="chip" aria-pressed={!filter.forge} onclick={() => setForge('')}>All</button>
    {#each forges as forge (forge.name)}
      <button
        type="button"
        class="chip"
        aria-pressed={filter.forge === forge.name}
        onclick={() => setForge(forge.name)}
      >
        {forgeLabel(forge.name)}
        {forge.count}
      </button>
    {/each}
  </div>

  {#if filter.forge}
    <div class="chips" role="group" aria-label="Owner">
      <span class="chips-label">Owner:</span>
      <button type="button" class="chip" aria-pressed={!filter.owner} onclick={() => setOwner('')}>All</button>
      {#each owners as owner (owner.name)}
        <button
          type="button"
          class="chip"
          aria-pressed={filter.owner === owner.name}
          onclick={() => setOwner(owner.name)}
        >
          {owner.name}
          {owner.count}
        </button>
      {/each}
    </div>
  {/if}

  <div class="picker">
    <span class="chips-label">Repos:</span>
    <button type="button" class="select" aria-expanded={open} onclick={() => (open ? close() : (open = true))}>
      {buttonLabel} ▾
    </button>

    {#if open}
      <div class="popover">
        <input class="search" type="search" placeholder="filter repos…" aria-label="filter repos" bind:value={search} />
        <ul class="options">
          <li class="all-repos">
            <label>
              <input
                type="checkbox"
                aria-label="All repos"
                checked={filter.repos.length === 0}
                onchange={() => setRepos([])}
              />
              All repos
            </label>
          </li>
          {#each groups as group (group.forge)}
            <li class="forge-group">
              <p class="forge-head">{forgeLabel(group.forge)}</p>
              <ul>
                {#each group.owners as ownerGroup (ownerGroup.owner)}
                  <li class="owner-group">
                    <p class="owner-head">
                      <span>{ownerGroup.owner}</span>
                      <label class="owner-all">
                        <input
                          type="checkbox"
                          aria-label={`all ${ownerGroup.owner}`}
                          checked={allSelected(ownerGroup)}
                          onchange={() => toggleOwner(ownerGroup)}
                        />
                        all
                      </label>
                    </p>
                    <ul>
                      {#each ownerGroup.repos as repo (repo.key)}
                        <li class="repo-row">
                          <label>
                            <input
                              type="checkbox"
                              aria-label={repo.label}
                              checked={filter.repos.includes(repo.key)}
                              onchange={() => toggleRepo(repo.key)}
                            />
                            <span class="name">{repo.name}</span>
                          </label>
                          {#if counts[repo.key]}<span class="count">{counts[repo.key]}</span>{/if}
                        </li>
                      {/each}
                    </ul>
                  </li>
                {/each}
              </ul>
            </li>
          {/each}
        </ul>
      </div>
    {/if}

    {#if isFiltered(filter)}
      <button type="button" class="clear link" onclick={() => clearFilter()}>✕ clear</button>
    {/if}
  </div>
</div>

<style>
  .repo-filter {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.35rem 1rem;
    padding: 0.4rem 1rem;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
  }
  .chips,
  .picker {
    display: flex;
    align-items: center;
    gap: 0.35rem;
  }
  .picker {
    position: relative;
    margin-left: auto;
  }
  .chips-label {
    color: var(--muted);
  }
  .chip {
    border-radius: 999px;
    padding: 0.15rem 0.7rem;
  }
  .chip[aria-pressed='true'] {
    border-color: var(--accent);
    font-weight: 600;
  }
  .popover {
    position: absolute;
    top: 100%;
    right: 0;
    z-index: 5;
    width: 20rem;
    max-height: 60vh;
    overflow-y: auto;
    margin-top: 0.25rem;
    padding: 0.5rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    box-shadow: 0 6px 18px rgb(0 0 0 / 0.18);
  }
  .search {
    width: 100%;
    padding: 0.3rem 0.4rem;
    background: var(--bg);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 6px;
  }
  .popover ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .options {
    margin-top: 0.4rem;
  }
  .forge-head {
    margin: 0.5rem 0 0.15rem;
    color: var(--muted);
    font-size: 0.8rem;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
  .owner-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    margin: 0.3rem 0 0.1rem;
    padding-left: 0.5rem;
    font-weight: 600;
  }
  .owner-all {
    font-weight: 400;
    color: var(--muted);
  }
  .repo-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    padding-left: 1rem;
  }
  .repo-row label {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    min-width: 0;
  }
  .repo-row .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .count {
    color: var(--muted);
    font-size: 0.85em;
  }
</style>
