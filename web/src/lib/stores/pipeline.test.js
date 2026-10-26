import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { get } from 'svelte/store'

vi.mock('./sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))

import { pipeline, startPipelinePolling } from './pipeline.js'

function stubBoard(getBoard) {
  const f = vi.fn(async () => ({ ok: true, status: 200, statusText: 'OK', json: async () => getBoard() }))
  vi.stubGlobal('fetch', f)
  return f
}

describe('startPipelinePolling', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    pipeline.set(null)
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('picks up a newer collectedAt on an unchanged board without any SSE event', async () => {
    let n = 0
    const f = stubBoard(() => ({ running: [], intervalsSeconds: { sessions: 5 }, collectedAt: { sessions: `t${++n}` } }))
    const stop = startPipelinePolling()

    await vi.advanceTimersByTimeAsync(5000)
    expect(get(pipeline).collectedAt.sessions).toBe('t1')
    await vi.advanceTimersByTimeAsync(5000)
    expect(get(pipeline).collectedAt.sessions).toBe('t2')
    expect(f).toHaveBeenCalledTimes(2)
    stop()
  })

  it('polls at the sessions interval, but never faster than 5 s', async () => {
    const f = stubBoard(() => ({ intervalsSeconds: { sessions: 1 } }))
    const stop = startPipelinePolling()
    await vi.advanceTimersByTimeAsync(5000)
    expect(f).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(4000)
    expect(f).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1000)
    expect(f).toHaveBeenCalledTimes(2)
    stop()
  })

  it('follows a longer sessions interval', async () => {
    const f = stubBoard(() => ({ intervalsSeconds: { sessions: 30 } }))
    const stop = startPipelinePolling()
    await vi.advanceTimersByTimeAsync(5000)
    expect(f).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(25000)
    expect(f).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(5000)
    expect(f).toHaveBeenCalledTimes(2)
    stop()
  })

  it('stops polling after the returned stop function is called', async () => {
    const f = stubBoard(() => ({ intervalsSeconds: { sessions: 5 } }))
    const stop = startPipelinePolling()
    stop()
    await vi.advanceTimersByTimeAsync(20000)
    expect(f).not.toHaveBeenCalled()
  })

  it('does not reschedule when stopped during an in-flight load', async () => {
    let release
    const f = vi.fn(() => new Promise((r) => { release = () => r({ ok: true, status: 200, json: async () => ({}) }) }))
    vi.stubGlobal('fetch', f)
    const stop = startPipelinePolling()
    await vi.advanceTimersByTimeAsync(5000)
    stop()
    release()
    await vi.advanceTimersByTimeAsync(20000)
    expect(f).toHaveBeenCalledTimes(1)
  })
})
