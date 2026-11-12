import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { get } from 'svelte/store'

// A hand-rolled sseEvent whose events the tests can emit. Hoisted, because
// the mock factory runs while ./repos.js is imported — before this file's own
// top-level statements.
const { sseEvent, emit } = vi.hoisted(() => {
  const subscribers = new Set()
  return {
    sseEvent: {
      subscribe: (fn) => {
        fn(null)
        subscribers.add(fn)
        return () => subscribers.delete(fn)
      },
    },
    emit: (event) => subscribers.forEach((fn) => fn(event)),
  }
})

vi.mock('./sse.js', () => ({ sseEvent }))

import { repos, loadRepos, REPOS_MAX_AGE_MS } from './repos.js'

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

// /api/repos walks the repos tree on disk, so a board update must not make it
// rescan. The store remembers its last successful load across these tests,
// which is why each one starts from its own, later clock.
describe('pipeline-updated', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    repos.set([])
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('reloads the repo list only once the last load is older than five minutes', async () => {
    vi.setSystemTime(new Date('2026-11-12T10:00:00Z'))
    const f = stubRepos([])
    await loadRepos()
    expect(f).toHaveBeenCalledTimes(1)

    vi.setSystemTime(Date.now() + 60_000)
    emit({ type: 'pipeline-updated' })
    expect(f).toHaveBeenCalledTimes(1)

    vi.setSystemTime(Date.now() + REPOS_MAX_AGE_MS + 1_000)
    emit({ type: 'pipeline-updated' })
    expect(f).toHaveBeenCalledTimes(2)
  })

  it('does not count a failed load as fresh', async () => {
    vi.setSystemTime(new Date('2026-11-12T12:00:00Z'))
    const f = vi.fn(async () => ({ ok: false, status: 500, statusText: 'boom' }))
    vi.stubGlobal('fetch', f)
    await loadRepos()
    expect(f).toHaveBeenCalledTimes(1)

    vi.setSystemTime(Date.now() + 60_000)
    emit({ type: 'pipeline-updated' })
    expect(f).toHaveBeenCalledTimes(2)
  })

  it('ignores every other event', async () => {
    vi.setSystemTime(new Date('2026-11-12T14:00:00Z'))
    const f = stubRepos([])
    emit({ type: 'inbox-updated' })
    expect(f).not.toHaveBeenCalled()
  })
})
