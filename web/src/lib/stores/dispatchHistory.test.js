import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

import { loadHistory, loadTick } from './dispatchHistory.js'

describe('dispatchHistory', () => {
  beforeEach(() => {
    globalThis.fetch = vi.fn(async () => ({ ok: true, json: async () => ({ summary: [], ticks: [] }) }))
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('asks for the given number of days', async () => {
    await loadHistory(30)
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/dispatch/history?days=30')
  })

  it('url-encodes the tick timestamp', async () => {
    await loadTick('2026-11-12T18:00:00+01:00')
    expect(globalThis.fetch).toHaveBeenCalledWith(
      '/api/dispatch/ticks/2026-11-12T18%3A00%3A00%2B01%3A00',
    )
  })

  it('propagates a failed request', async () => {
    globalThis.fetch = vi.fn(async () => ({ ok: false, status: 404, statusText: 'Not Found' }))
    await expect(loadTick('2026-11-12T18:00:00Z')).rejects.toThrow(/404/)
  })
})
