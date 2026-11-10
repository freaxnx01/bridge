export function ageSeconds(collectedAt, now) {
  if (!collectedAt) return null
  const ms = now - new Date(collectedAt).getTime()
  return Math.max(0, Math.floor(ms / 1000))
}

export function formatAge(seconds) {
  if (seconds === null || seconds === undefined) return ''
  if (seconds < 60) return `${seconds}s`
  const mins = Math.floor(seconds / 60)
  if (mins < 60) return `${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}

export function isStale(collectedAt, intervalSeconds, now) {
  const age = ageSeconds(collectedAt, now)
  if (age === null || !intervalSeconds) return false
  return age > 2 * intervalSeconds
}

// oldestSource picks, among the named sources that have been collected, the
// one with the oldest collectedAt. Returns null when none has been collected.
export function oldestSource(board, names) {
  let oldest = null
  for (const name of names) {
    const at = board?.collectedAt?.[name]
    if (!at) continue
    if (!oldest || new Date(at) < new Date(oldest.collectedAt)) {
      oldest = { name, collectedAt: at, intervalSeconds: board.intervalsSeconds?.[name] }
    }
  }
  return oldest
}

export function staleSourceCount(board, now) {
  if (!board) return 0
  return Object.keys(board.collectedAt ?? {}).filter((name) =>
    isStale(board.collectedAt[name], board.intervalsSeconds?.[name], now),
  ).length
}
