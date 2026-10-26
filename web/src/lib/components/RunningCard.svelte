<script>
  import { ageSeconds, formatAge } from '../freshness.js';

  let { card, now } = $props();

  const icon = $derived(
    card.kind === 'pipeline' ? '▶' : card.kind === 'enrichment' ? '✎' : card.status === 'idle' ? '○' : '●',
  );
  const shortRepo = $derived((card.repo ?? '').split('/').pop());
  const worktree = $derived(card.cwd ? card.cwd.split('/').filter(Boolean).pop() : '');
  const subject = $derived(
    card.issue
      ? `#${card.issue}`
      : card.kind === 'session' && worktree && worktree !== shortRepo
        ? `[${worktree}]`
        : card.title,
  );
  const age = $derived(formatAge(ageSeconds(card.since, now)));
  const link = $derived(openLink(card));

  function openLink(c) {
    if (c.kind === 'session') {
      return c.sessionId?.startsWith('session_') ? { href: `https://claude.ai/code/${c.sessionId}`, text: 'Open RC ↗' } : null;
    }
    if (c.kind === 'enrichment') {
      if (c.url) return { href: c.url, text: 'Open ↗' };
      return c.issue && c.repo?.includes('/')
        ? { href: `https://github.com/${c.repo}/issues/${c.issue}`, text: 'Open ↗' }
        : null;
    }
    return c.url ? { href: c.url, text: 'Open ↗' } : null;
  }
</script>

<li class="card">
  <span class="icon" aria-hidden="true">{icon}</span>
  <div class="main">
    <div class="line1"><span class="kind">{card.kind}</span> <strong>{shortRepo}</strong> {subject}</div>
    <div class="line2">{card.kind === 'pipeline' ? card.title : card.status} · {age}</div>
  </div>
  {#if link}<a class="action" href={link.href} target="_blank" rel="noopener noreferrer">{link.text}</a>{/if}
</li>
