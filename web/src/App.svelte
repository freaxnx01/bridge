<script>
  import './app.css';
  import { onMount } from 'svelte';
  import Header from './lib/components/Header.svelte';
  import RepoFilterBar from './lib/components/RepoFilterBar.svelte';
  import TokenDialog from './lib/components/TokenDialog.svelte';
  import Inbox from './routes/Inbox.svelte';
  import Pipeline from './routes/Pipeline.svelte';
  import { pipeline } from './lib/stores/pipeline.js';
  import { inbox, loadInbox, startInboxPolling } from './lib/stores/inbox.js';
  import { repos, loadRepos } from './lib/stores/repos.js';
  import { repoFilter, initRepoFilter, repoKey, matches } from './lib/stores/repoFilter.js';
  import { now } from './lib/stores/now.js';
  import { requestToken } from './lib/stores/tokenPrompt.js';
  import { staleSourceCount } from './lib/freshness.js';
  import architectureSvg from './lib/architecture.svg?raw';

  let area = $state('inbox');

  const staleCount = $derived(staleSourceCount($pipeline, $now));
  const inboxCount = $derived(($inbox?.cards ?? []).filter((card) => matches($repoFilter, card)).length);

  // Every item the current tab can show, so the dropdown can put a count next
  // to each repo.
  const areaItems = $derived.by(() => {
    if (area === 'inbox') return [...($inbox?.cards ?? []), ...($inbox?.waiting ?? []), ...($inbox?.parked ?? [])];
    if (area === 'pipeline') {
      const board = $pipeline;
      return [...(board?.running ?? []), ...(board?.ready ?? []), ...(board?.held ?? []), ...(board?.needsEnrichment ?? [])];
    }
    return [];
  });
  const counts = $derived.by(() => {
    const perRepo = {};
    for (const item of areaItems) {
      if (!item.repo) continue;
      const key = repoKey(item);
      perRepo[key] = (perRepo[key] ?? 0) + 1;
    }
    return perRepo;
  });

  // The inbox loads for every area, so the header badge stays live off the
  // Inbox page too.
  onMount(() => {
    loadInbox();
    loadRepos();
    const stopFilter = initRepoFilter();
    const stopPolling = startInboxPolling();
    return () => {
      stopFilter();
      stopPolling();
    };
  });
</script>

<Header
  {area}
  onnavigate={(next) => (area = next)}
  {staleCount}
  dispatchPaused={$pipeline?.dispatchPaused ?? false}
  {inboxCount}
  onToken={() => requestToken()}
/>

<!-- The Architecture tab shows a static diagram: there is nothing to filter. -->
{#if area !== 'arch'}
  <RepoFilterBar repos={$repos} filter={$repoFilter} {counts} />
{/if}

<main>
  {#if area === 'inbox'}
    <Inbox />
  {:else if area === 'pipeline'}
    <Pipeline />
  {:else}
    <section class="arch">
      <h2>Architecture</h2>
      <p>How ai-instructions, bridge, and agent-pipeline fit together:</p>
      {@html architectureSvg}
    </section>
  {/if}
</main>

<TokenDialog />
