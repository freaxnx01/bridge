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

  function shortAge(startedAt) {
    if (!startedAt) return '';
    const ms = Date.now() - new Date(startedAt).getTime();
    if (!(ms >= 0)) return '';
    const mins = Math.floor(ms / 60000);
    if (mins < 60) return `${mins}m`;
    const hours = Math.floor(mins / 60);
    if (hours < 24) return `${hours}h`;
    return `${Math.floor(hours / 24)}d`;
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
        {#each $agents as a (a.sessionId)}
          <li>
            <strong>{a.name}</strong> · {a.status} · {a.kind} · {shortRepo(a.cwd)} · {shortAge(a.startedAt)}
          </li>
        {/each}
      </ul>
    {/if}
  </section>
</main>
