<script>
  import { tick } from 'svelte';
  import { tokenPrompt } from '../stores/tokenPrompt.js';
  import { setToken } from '../token.js';

  let value = $state('');
  let input = $state();

  $effect(() => {
    if ($tokenPrompt) tick().then(() => input?.focus());
  });

  function finish(token) {
    const prompt = $tokenPrompt;
    tokenPrompt.set(null);
    value = '';
    prompt?.resolve(token);
  }

  function save(event) {
    event.preventDefault();
    const token = value.trim();
    if (!token) return;
    setToken(token);
    finish(token);
  }

  function onkeydown(event) {
    if (event.key === 'Escape') finish(null);
  }
</script>

{#if $tokenPrompt}
  <div class="backdrop">
    <form class="dialog" role="dialog" aria-modal="true" aria-labelledby="token-title" {onkeydown} onsubmit={save}>
      <h2 id="token-title">API token</h2>
      <p>Needed for actions. Stored in this browser only.</p>
      {#if $tokenPrompt.rejected}<p class="error" role="alert">Token rejected</p>{/if}
      <label>
        <span class="visually-hidden">Token</span>
        <input type="password" autocomplete="off" bind:this={input} bind:value />
      </label>
      <div class="buttons">
        <button type="button" onclick={() => finish(null)}>Cancel</button>
        <button type="submit" class="primary">Save</button>
      </div>
    </form>
  </div>
{/if}
