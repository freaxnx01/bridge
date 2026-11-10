<script>
  import './app.css';
  import Header from './lib/components/Header.svelte';
  import TokenDialog from './lib/components/TokenDialog.svelte';
  import Pipeline from './routes/Pipeline.svelte';
  import { pipeline } from './lib/stores/pipeline.js';
  import { now } from './lib/stores/now.js';
  import { requestToken } from './lib/stores/tokenPrompt.js';
  import { staleSourceCount } from './lib/freshness.js';
  import architectureSvg from './lib/architecture.svg?raw';

  let area = $state('pipeline');

  const staleCount = $derived(staleSourceCount($pipeline, $now));
</script>

<Header
  {area}
  onnavigate={(next) => (area = next)}
  {staleCount}
  dispatchPaused={$pipeline?.dispatchPaused ?? false}
  onToken={() => requestToken()}
/>

<main>
  {#if area === 'pipeline'}
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
