<script>
  import { onMount } from 'svelte';
  import { loadRepos, repos } from './lib/stores/repos.js';
  import { loadAgents, agents } from './lib/stores/agents.js';

  onMount(() => { loadRepos(); loadAgents(); });

  function shortRepo(cwd) {
    if (!cwd) return '';
    const parts = cwd.split('/').filter(Boolean);
    return parts.slice(-2).join('/');
  }
</script>

<main>
  <h1>Bridge WebUI</h1>
  <p>{$repos.length} repos loaded.</p>

  <section>
    <h2>Agents · {$agents.length}</h2>
    {#if $agents.length === 0}
      <p>No live Claude sessions.</p>
    {:else}
      <ul>
        {#each $agents as a}
          <li>
            <strong>{a.name}</strong> · {a.status} · {a.kind} · {shortRepo(a.cwd)}
          </li>
        {/each}
      </ul>
    {/if}
  </section>
</main>
