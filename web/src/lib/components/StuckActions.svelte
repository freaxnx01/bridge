<script>
  import { authedPost, ensureToken } from '../auth.js';
  import { loadPipeline } from '../stores/pipeline.js';
  import ConfirmDialog from './ConfirmDialog.svelte';

  let { card } = $props();

  const ROUTES = { 'clear-lock': '/api/pipeline/clear-lock', 'clear-reenrich': '/api/pipeline/clear-reenrich' };
  const LABELS = { 'clear-lock': 'Clear lock', 'clear-reenrich': 'Clear & re-enrich' };

  // idle | confirm | working | done | failed
  let state = $state('idle');
  let action = $state('');
  let outcome = $state('');
  let failure = $state('');
  let token = null;

  async function start(next) {
    token = await ensureToken();
    if (!token) return;
    action = next;
    state = 'confirm';
  }

  async function confirm() {
    state = 'working';
    try {
      const res = await authedPost(ROUTES[action], { repo: card.repo, number: card.issue }, token);
      outcome = successText(res);
      state = 'done';
      loadPipeline();
    } catch (err) {
      failure = failureText(err);
      state = 'failed';
      // A failed re-enrich launch has already cleared the lock, so the board moved on.
      if (err.status !== 401 && err.status !== 403) loadPipeline();
    }
  }

  function successText(res) {
    if (action === 'clear-lock') return 'Lock cleared';
    return res.alreadyRunning ? 'Lock cleared · enrich already running' : `Lock cleared · enrich started in ${res.slot}`;
  }

  function failureText(err) {
    if (err.status === 401) return 'Token rejected';
    if (err.status === 403) return 'Blocked: wrong origin';
    if (err.status === 409) return `Not actionable (${err.message})`;
    if (err.status === 422) return err.message;
    return err.message || 'request failed';
  }
</script>

{#each Object.keys(ROUTES) as a (a)}
  <button type="button" class="action" disabled={state === 'working'} onclick={() => start(a)}>
    {state === 'working' && action === a ? 'working…' : LABELS[a]}
  </button>
{/each}
{#if state === 'done'}
  <span class="result ok">✓ {outcome}</span>
{:else if state === 'failed'}
  <span class="result error" role="alert">✗ {failure} <button type="button" class="link" onclick={() => (state = 'idle')}>dismiss</button></span>
{/if}
{#if state === 'confirm'}
  <ConfirmDialog repo={card.repo} number={card.issue} {action} onconfirm={confirm} oncancel={() => (state = 'idle')} />
{/if}
