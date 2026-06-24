<script>
  import { onMount } from 'svelte';
  import { loadRepos, repos } from './lib/stores/repos.js';

  onMount(() => { loadRepos(); });
</script>

<main>
  <h1>Bridge WebUI</h1>
  <p>{$repos.length} repos loaded.</p>
</main>
