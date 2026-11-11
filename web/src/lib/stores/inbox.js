import { writable } from 'svelte/store'
import { get as apiGet } from '../api.js'
import { sseEvent } from './sse.js'

export const inbox = writable(null)
export const inboxError = writable(null)

export async function loadInbox() {
  try {
    inbox.set(await apiGet('/api/inbox'))
    inboxError.set(null)
  } catch (err) {
    inboxError.set(err.message)
  }
}

// loadIssueDetail reads one issue's run history live. repo is "owner/name".
export function loadIssueDetail(repo, number) {
  return apiGet(`/api/inbox/${repo}/${number}`)
}

sseEvent.subscribe((ev) => {
  if (ev?.type === 'inbox-updated') loadInbox()
})

// INBOX_POLL_SECONDS is the fallback for a missed SSE event. It is fixed
// rather than derived from the board's intervals: the inbox has no per-source
// freshness to keep honest, so a minute is enough.
const INBOX_POLL_SECONDS = 60

// startInboxPolling re-fetches the inbox every minute. Returns the stop function.
export function startInboxPolling() {
  let timer = null
  let stopped = false
  const tick = async () => {
    await loadInbox()
    if (!stopped) timer = setTimeout(tick, INBOX_POLL_SECONDS * 1000)
  }
  timer = setTimeout(tick, INBOX_POLL_SECONDS * 1000)
  return () => {
    stopped = true
    clearTimeout(timer)
  }
}
