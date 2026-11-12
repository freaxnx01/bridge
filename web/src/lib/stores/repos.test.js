import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { get } from 'svelte/store'

vi.mock('./sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))

import { repos, loadRepos } from './repos.js'

function stubRepos(list) {
  const f = vi.fn(async () => ({ ok: true, status: 200, statusText: 'OK', json: async () => list }))
  vi.stubGlobal('fetch', f)
  return f
}

describe('loadRepos', () => {
  beforeEach(() => repos.set([]))
  afterEach(() => vi.unstubAllGlobals())

  it('keeps forge, owner and name of every discovered repo', async () => {
    const f = stubRepos([
      { name: 'bridge', path: '/r/bridge', forge: 'github', owner: 'freaxnx01', visibility: 'public' },
      { name: 'sandbox', path: '/r/sandbox', forge: 'ado', owner: 'personal', visibility: 'private' },
    ])
    await loadRepos()

    expect(f).toHaveBeenCalledWith('/api/repos')
    expect(get(repos)).toEqual([
      { forge: 'github', owner: 'freaxnx01', name: 'bridge' },
      { forge: 'ado', owner: 'personal', name: 'sandbox' },
    ])
  })

  it('keeps the last known list when the request fails', async () => {
    repos.set([{ forge: 'github', owner: 'o', name: 'a' }])
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false, status: 500, statusText: 'boom' })))

    await loadRepos()

    expect(get(repos)).toEqual([{ forge: 'github', owner: 'o', name: 'a' }])
  })
})
