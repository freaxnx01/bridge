import { get as apiGet } from '../api.js'

// loadHistory reads the dispatcher's own record of the last `days` days: the
// per-issue summary and the tick timeline.
export async function loadHistory(days) {
  return apiGet(`/api/dispatch/history?days=${days}`)
}

// loadTick reads one tick's whole record. The timestamp is encoded because an
// RFC3339 instant carries colons and may carry a "+" offset.
export async function loadTick(at) {
  return apiGet(`/api/dispatch/ticks/${encodeURIComponent(at)}`)
}
