import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { get } from 'svelte/store'

vi.mock('./sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))

import { inbox, startInboxPolling, loadIssueDetail } from './inbox.js'

function stubJson(body) {
  const f = vi.fn(async () => ({ ok: true, status: 200, statusText: 'OK', json: async () => body }))
  vi.stubGlobal('fetch', f)
  return f
}

describe('startInboxPolling', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    inbox.set(null)
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('fetches /api/inbox once per 60 s', async () => {
    const f = stubJson({ cards: [], waiting: [], parked: [] })
    const stop = startInboxPolling()

    await vi.advanceTimersByTimeAsync(59000)
    expect(f).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1000)
    expect(f).toHaveBeenCalledWith('/api/inbox')
    expect(f).toHaveBeenCalledTimes(1)
    expect(get(inbox).cards).toEqual([])

    await vi.advanceTimersByTimeAsync(60000)
    expect(f).toHaveBeenCalledTimes(2)
    stop()
  })

  it('stops polling after the returned stop function is called', async () => {
    const f = stubJson({ cards: [] })
    const stop = startInboxPolling()
    stop()
    await vi.advanceTimersByTimeAsync(180000)
    expect(f).not.toHaveBeenCalled()
  })
})

describe('loadIssueDetail', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('fetches the issue detail endpoint', async () => {
    const f = stubJson({ issue: {}, runs: [] })
    await loadIssueDetail('o/r', 5)
    expect(f).toHaveBeenCalledWith('/api/inbox/o/r/5')
  })
})
