<script>
  import { authedPost, ensureToken } from '../auth.js';
  import { loadPipeline } from '../stores/pipeline.js';
  import ConfirmDialog from './ConfirmDialog.svelte';

  let { card, action } = $props();

  // idle | confirm | launching | launched | alreadyRunning | failed
  let state = $state('idle');
  let result = $state(null);
  let failure = $state('');
  let token = null;

  const label = $derived(action === 'enrich' ? 'Enrich' : 'Dispatch');
  const shortRepo = $derived(card.repo.split('/').pop());
  const ghOnly = $derived(action === 'enrich' && card.forge !== 'github');
  const type = $derived((card.title.match(/^(\w+)(\([^)]*\))?!?:/) ?? [])[1]);
  const size = $derived((card.labels ?? []).find((l) => l.startsWith('size:')));
  const meta = $derived([type, size, card.forge !== 'github' ? card.forge : null].filter(Boolean).join(' · '));

  // The live dispatch preview's answer for this card. "next" means the next
  // acting tick would dispatch it; a skip always names the gate that stopped it.
  const verdictText = $derived.by(() => {
    if (card.verdict === 'next') return 'next';
    return card.verdictReason ? `skip: ${card.verdictReason}` : 'skip';
  });

  async function start() {
    token = await ensureToken();
    if (token) state = 'confirm';
  }

  async function confirm() {
    state = 'launching';
    try {
      result = await authedPost(`/api/pipeline/${action}`, { repo: card.repo, number: card.number }, token);
      state = result.alreadyRunning ? 'alreadyRunning' : 'launched';
    } catch (err) {
      failure = failureText(err);
      state = 'failed';
      if (err.status === 409 || err.status === 422) loadPipeline();
    }
  }

  function failureText(err) {
    if (err.status === 401) return 'Token rejected';
    if (err.status === 403) return 'Blocked: wrong origin';
    if (err.status === 409) return `Not on the board any more (${err.message})`;
    if (err.status === 422) return err.message;
    return err.message || 'launch failed';
  }
</script>

<li class="card">
  <div class="main">
    <div class="line1">
      <a href={card.url} target="_blank" rel="noopener noreferrer"><strong>{shortRepo}</strong> #{card.number}</a>
      <span class="title">{card.title}</span>
    </div>
    <div class="line2">
      {meta}{#if card.reason}{meta ? ' · ' : ''}<span class="reason">{card.reason}</span>{/if}
    </div>
    {#if card.verdict}
      <div class="line2 verdict">
        <span class={card.verdict === 'next' ? 'verdict-next' : 'verdict-skip'}>{verdictText}</span>
        {#if card.lane}<span class="lane">lane {card.lane}</span>{/if}
        {#if card.laneReason}<span class="reason">{card.laneReason}</span>{/if}
      </div>
    {/if}
    {#if state === 'launched'}
      <div class="result ok">✓ Started · {result.slot}{#if result.rcUrl} · <a href={result.rcUrl} target="_blank" rel="noopener noreferrer">Open RC ↗</a>{/if}</div>
    {:else if state === 'alreadyRunning'}
      <div class="result">Already running{#if result.rcUrl} · <a href={result.rcUrl} target="_blank" rel="noopener noreferrer">Open RC ↗</a>{/if}</div>
    {:else if state === 'failed'}
      <div class="result error" role="alert">✗ {failure} <button type="button" class="link" onclick={() => (state = 'idle')}>dismiss</button></div>
    {/if}
  </div>
  {#if ghOnly}<span class="muted">(GH only)</span>{/if}
  {#if action}
    <button type="button" class="action" disabled={ghOnly || state === 'launching'} onclick={start}>
      {state === 'launching' ? 'launching…' : label}
    </button>
  {/if}
  {#if state === 'confirm'}
    <ConfirmDialog repo={card.repo} number={card.number} {action} onconfirm={confirm} oncancel={() => (state = 'idle')} />
  {/if}
</li>

<style>
  .verdict {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4em;
  }
  .verdict-next {
    font-weight: 700;
    color: var(--ok, #2aa198);
  }
  .verdict-skip {
    color: var(--muted, #93a1a1);
  }
  .lane {
    opacity: 0.8;
  }
</style>
