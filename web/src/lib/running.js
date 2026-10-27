import { ageSeconds } from './freshness.js'

// An enrichment-ongoing label whose issue hasn't changed for this long is a
// leftover from a run that ended without clearing it, not work in flight.
export const STUCK_ENRICHING_SECONDS = 6 * 3600

// splitRunning separates the Running column into work actually in flight and
// the two kinds of noise that would otherwise bury it: idle Claude sessions
// and stuck enrichment labels. Input order is kept within each group.
export function splitRunning(running, now) {
  const groups = { active: [], idle: [], stuck: [] }
  for (const card of running ?? []) {
    groups[groupOf(card, now)].push(card)
  }
  return groups
}

function groupOf(card, now) {
  if (card.kind === 'session' && card.status === 'idle') return 'idle'
  if (card.kind === 'enrichment' && ageSeconds(card.since, now) > STUCK_ENRICHING_SECONDS) return 'stuck'
  return 'active'
}
