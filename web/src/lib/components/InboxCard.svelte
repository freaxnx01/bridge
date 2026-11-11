<script>
  import { shortRef, prUrl, runMeta } from '../inbox.js';
  import { ageSeconds, formatAge } from '../freshness.js';

  let { card, now, onopen } = $props();

  const glyph = $derived(card.kind === 'retry' ? '↻' : '●');
  const age = $derived(formatAge(ageSeconds(card.since, now)));
  const meta = $derived(runMeta(card.run));
</script>

<li class="inbox-card">
  <button type="button" class="open" onclick={() => onopen(card)}>
    <span class="head">
      <span class="ref"><span class="glyph" class:retry={card.kind === 'retry'}>{glyph}</span> {shortRef(card)}</span>
      <span class="age">{age}</span>
    </span>
    <span class="title">{card.title}</span>
    <strong class="reason">{card.reason}</strong>
    {#if meta}<span class="meta">{meta}</span>{/if}
  </button>
  <div class="links">
    <a href={card.url} target="_blank" rel="noopener noreferrer">Issue ↗</a>
    {#if card.run?.pr > 0}
      <a href={prUrl(card.repo, card.run.pr)} target="_blank" rel="noopener noreferrer">PR ↗</a>
    {/if}
    {#if card.run?.runUrl}
      <a href={card.run.runUrl} target="_blank" rel="noopener noreferrer">Run ↗</a>
    {/if}
  </div>
</li>

<style>
  .inbox-card {
    list-style: none;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 6px;
    overflow: hidden;
  }
  .open {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    width: 100%;
    min-height: 44px;
    padding: 0.6rem 0.75rem;
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .head {
    display: flex;
    justify-content: space-between;
    gap: 0.5rem;
  }
  .ref {
    font-weight: 600;
  }
  .glyph {
    color: var(--error);
  }
  .glyph.retry {
    color: var(--warn);
  }
  .age,
  .meta {
    color: var(--muted);
    font-size: 0.85em;
  }
  .title,
  .reason {
    overflow-wrap: anywhere;
  }
  .links {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 1rem;
    padding: 0 0.75rem 0.5rem;
  }
  .links a {
    display: inline-flex;
    align-items: center;
    min-height: 44px;
    font-size: 0.9em;
  }
</style>
