<script>
  import './app.css';
  import { onMount } from 'svelte';
  import Header from './lib/components/Header.svelte';
  import TokenDialog from './lib/components/TokenDialog.svelte';
  import Inbox from './routes/Inbox.svelte';
  import Pipeline from './routes/Pipeline.svelte';
  import { pipeline } from './lib/stores/pipeline.js';
  import { inbox, loadInbox, startInboxPolling } from './lib/stores/inbox.js';
  import { now } from './lib/stores/now.js';
  import { requestToken } from './lib/stores/tokenPrompt.js';
  import { staleSourceCount } from './lib/freshness.js';
  import architectureSvg from './lib/architecture.svg?raw';

  let area = $state('inbox');

  const staleCount = $derived(staleSourceCount($pipeline, $now));

  // The inbox loads for every area, so the header badge stays live off the
  // Inbox page too.
  onMount(() => {
    loadInbox();
    return startInboxPolling();
  });
</script>

<Header
  {area}
  onnavigate={(next) => (area = next)}
  {staleCount}
  dispatchPaused={$pipeline?.dispatchPaused ?? false}
  inboxCount={$inbox?.cards?.length ?? 0}
  onToken={() => requestToken()}
/>

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
