<script>
  import { clearFilter } from '../stores/repoFilter.js';
</script>

<p class="filter-empty">
  Nothing for the selected repos.
  <button type="button" class="link" onclick={() => clearFilter()}>✕ clear filter</button>
</p>

<style>
  .filter-empty {
    color: var(--muted);
  }
</style>
