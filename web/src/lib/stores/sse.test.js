import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

class MockEventSource {
  constructor(url) {
    this.url = url
    MockEventSource.lastInstance = this
  }
  set onmessage(fn) { this._onmessage = fn }
  set onerror(fn) { this._onerror = fn }
  close() {}
  trigger(data) { this._onmessage?.({ data: JSON.stringify(data) }) }
}

describe('createSseStore', () => {
  beforeEach(() => {
    vi.stubGlobal('EventSource', MockEventSource)
    vi.stubGlobal('window', {})
  })
  afterEach(() => vi.unstubAllGlobals())

  it('updates store value on valid message', async () => {
    const { createSseStore } = await import('./sse.js?t=' + Date.now())
    const store = createSseStore('/api/events')

    let received = null
    store.subscribe(v => { received = v })

    MockEventSource.lastInstance.trigger({ type: 'repo-updated', data: { name: 'foo' } })

    expect(received?.type).toBe('repo-updated')
    store.disconnect()
  })

  it('ignores invalid JSON without throwing', async () => {
    const { createSseStore } = await import('./sse.js?t=' + Date.now())
    const store = createSseStore('/api/events')
    expect(() => {
      MockEventSource.lastInstance._onmessage?.({ data: 'not-json' })
    }).not.toThrow()
    store.disconnect()
  })
})
