<script>
  import InboxCard from '../lib/components/InboxCard.svelte';
  import IssueDetail from '../lib/components/IssueDetail.svelte';
  import WaitLine from '../lib/components/WaitLine.svelte';
  import { inbox, inboxError } from '../lib/stores/inbox.js';
  import { now } from '../lib/stores/now.js';

  let parkedOpen = $state(false);
  let selected = $state(null);

  const data = $derived($inbox);
  const loadError = $derived(data ? null : $inboxError);
  const cards = $derived(data?.cards ?? []);
  const waiting = $derived(data?.waiting ?? []);
  const parked = $derived(data?.parked ?? []);
  const errors = $derived(Object.entries(data?.errors ?? {}));

  function cardKey(card) {
    return `${card.repo}#${card.number}`;
  }

  // The payload keys a failed comment read "comments owner/name#5", like the
  // board's source errors; the warning line already says what failed.
  function issueRef(key) {
    return key.replace(/^comments /, '');
  }
</script>

<div class="inbox">
  {#if loadError}
    <p class="load-error" role="alert">{loadError}</p>
  {/if}

  <section class="needs">
    {#if cards.length === 0}
      <p class="empty">Nothing needs you.</p>
    {:else}
      <ul class="cards">
        {#each cards as card (cardKey(card))}
          <InboxCard {card} now={$now} onopen={(c) => (selected = c)} />
        {/each}
      </ul>
    {/if}

    {#if parked.length > 0}
      <button type="button" class="collapse" aria-expanded={parkedOpen} onclick={() => (parkedOpen = !parkedOpen)}>
        {parkedOpen ? '▾' : '▸'} Parked ({parked.length})
      </button>
      {#if parkedOpen}
        <ul class="cards">
          {#each parked as card (cardKey(card))}
            <InboxCard {card} now={$now} onopen={(c) => (selected = c)} />
          {/each}
        </ul>
      {/if}
    {/if}
  </section>

  <section class="waiting">
    <h2>Running &amp; waiting</h2>
    {#if waiting.length === 0}
      <p class="empty">Nothing running.</p>
    {:else}
      <ul class="lines">
        {#each waiting as card (cardKey(card))}
          <WaitLine {card} now={$now} onopen={(c) => (selected = c)} />
        {/each}
      </ul>
    {/if}
    {#each errors as [key, message] (key)}
      <p class="warn" role="status">⚠ Could not read comments: {issueRef(key)} — {message}</p>
    {/each}
  </section>

  {#if selected}
    <IssueDetail repo={selected.repo} number={selected.number} onclose={() => (selected = null)} />
  {/if}
</div>

<style>
  .inbox {
    display: grid;
    gap: 1rem;
  }
  .cards {
    display: grid;
    gap: 0.5rem;
    margin: 0;
    padding: 0;
  }
  .lines {
    margin: 0;
    padding: 0;
  }
  h2 {
    margin: 0 0 0.5rem;
    font-size: 0.95rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--muted);
  }
  .empty,
  .load-error {
    color: var(--muted);
  }
  .load-error {
    color: var(--error);
  }
  .collapse {
    min-height: 44px;
    margin-top: 0.75rem;
    border-color: transparent;
    background: none;
  }
  @media (min-width: 900px) {
    .inbox {
      grid-template-columns: 2fr 1fr;
      align-items: start;
    }
    .cards {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
</style>
