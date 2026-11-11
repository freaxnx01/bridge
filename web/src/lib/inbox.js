// shortRef is how a card names its issue: the repo name without the owner,
// plus the number — "bridge#412".
export function shortRef(card) {
  return `${card.repo.split('/').pop()}#${card.number}`
}

export function prUrl(repo, pr) {
  return `https://github.com/${repo}/pull/${pr}`
}

// runMeta is the one-line summary of a run record. Empty parts are dropped;
// the attempt only shows when the pipeline counted one.
export function runMeta(run) {
  if (!run) return ''
  return [run.agent, run.model, `$${(run.costUsd ?? 0).toFixed(2)}`, run.attempt > 0 ? `#${run.attempt}` : '']
    .filter(Boolean)
    .join(' · ')
}
