<script>
  import { shortRef } from '../inbox.js';
  import { ageSeconds, formatAge } from '../freshness.js';

  let { card, now, onopen } = $props();

  const age = $derived(formatAge(ageSeconds(card.since, now)));
</script>

<li>
  <button type="button" class="wait-line" onclick={() => onopen(card)}>
    <span class="ref">{shortRef(card)}</span>
    <span class="reason">{card.reason}</span>
    <span class="age">{age}</span>
  </button>
</li>

<style>
  li {
    list-style: none;
  }
  .wait-line {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    min-height: 44px;
    padding: 0.25rem 0.5rem;
    border: 0;
    border-bottom: 1px solid var(--border);
    background: none;
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .ref {
    font-weight: 600;
  }
  .reason {
    flex: 1;
    color: var(--muted);
    overflow-wrap: anywhere;
  }
  .age {
    color: var(--muted);
    font-size: 0.85em;
  }
</style>
