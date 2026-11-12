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
  if (isStuckEnrichment(card, now)) return 'stuck'
  return 'active'
}

// isStuckEnrichment: an enrichment-ongoing label older than STUCK_ENRICHING_SECONDS.
export function isStuckEnrichment(card, now) {
  return card.kind === 'enrichment' && ageSeconds(card.since, now) > STUCK_ENRICHING_SECONDS
}

// A dispatched pipeline run normally finishes, review included, in 10-25
// minutes. Past RUN_WARN_MINUTES it is unusually slow, past RUN_OVER_MINUTES
// it is most likely stuck.
export const RUN_WARN_MINUTES = 30
export const RUN_OVER_MINUTES = 60

// runDuration returns how many whole minutes a card has been running and how
// alarming that is: 'ok', 'warn' or 'over'.
export function runDuration(card, now) {
  const minutes = Math.floor(ageSeconds(card.since, now) / 60)
  if (minutes >= RUN_OVER_MINUTES) return { minutes, level: 'over' }
  if (minutes >= RUN_WARN_MINUTES) return { minutes, level: 'warn' }
  return { minutes, level: 'ok' }
}
