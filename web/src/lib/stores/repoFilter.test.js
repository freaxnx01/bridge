import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'

import {
  repoFilter, ownerOf, repoKey, matches, isFiltered,
  setForge, setOwner, toggleRepo, setRepos, clearFilter,
  parseQuery, toQuery, initRepoFilter,
} from './repoFilter.js'

const item = (forge, repo) => ({ forge, repo })
const noFilter = { forge: '', owner: '', repos: [] }

// fakeWin is the slice of `window` the store touches: a location, a
// replaceState spy and a localStorage that can be made to throw.
function fakeWin({ search = '', stored = null, throws = false } = {}) {
  const calls = []
  const data = new Map()
  if (stored !== null) data.set('bridge.repoFilter', stored)
  return {
    location: { pathname: '/', search, hash: '' },
    history: { replaceState: vi.fn((state, title, url) => calls.push(url)) },
    localStorage: {
      getItem: (k) => {
        if (throws) throw new Error('denied')
        return data.has(k) ? data.get(k) : null
      },
      setItem: (k, v) => {
        if (throws) throw new Error('denied')
        data.set(k, v)
      },
    },
    urls: calls,
    read: (k) => (data.has(k) ? data.get(k) : null),
  }
}

describe('ownerOf', () => {
  it('is the repo up to the last slash', () => {
    expect(ownerOf('o/a')).toBe('o')
    expect(ownerOf('DMS cross-customer projects/r')).toBe('DMS cross-customer projects')
    expect(ownerOf('')).toBe('')
  })
})

describe('repoKey', () => {
  it('keys an item and an /api/repos entry the same way', () => {
    expect(repoKey(item('github', 'o/a'))).toBe('github:o/a')
    expect(repoKey({ forge: 'github', owner: 'o', name: 'a' })).toBe('github:o/a')
  })
})

describe('matches', () => {
  it('shows everything, including a repo-less card, with no filter', () => {
    expect(matches(noFilter, item('github', 'o/a'))).toBe(true)
    expect(matches(noFilter, item('', ''))).toBe(true)
  })

  it('hides items of other forges', () => {
    const f = { forge: 'github', owner: '', repos: [] }
    expect(matches(f, item('github', 'o/a'))).toBe(true)
    expect(matches(f, item('ado', 'o/a'))).toBe(false)
  })

  it('hides items of other owners', () => {
    const f = { forge: 'github', owner: 'anim-bossinfo-ch', repos: [] }
    expect(matches(f, item('github', 'freaxnx01/bridge'))).toBe(false)
    expect(matches(f, item('github', 'anim-bossinfo-ch/X'))).toBe(true)
  })

  it('matches an ADO project owner with spaces', () => {
    const f = { forge: 'ado', owner: 'DMS cross-customer projects', repos: [] }
    expect(matches(f, item('ado', 'DMS cross-customer projects/r'))).toBe(true)
  })

  it('keeps same-named repos on two forges apart', () => {
    const f = { forge: '', owner: '', repos: ['github:o/a'] }
    expect(matches(f, item('github', 'o/a'))).toBe(true)
    expect(matches(f, item('ado', 'o/a'))).toBe(false)
  })

  it('hides a card outside every repo as soon as anything is filtered', () => {
    expect(matches({ forge: 'github', owner: '', repos: [] }, item('', ''))).toBe(false)
    expect(matches({ forge: '', owner: '', repos: ['github:o/a'] }, item('', ''))).toBe(false)
  })
})

describe('isFiltered', () => {
  it('is false only for the empty filter', () => {
    expect(isFiltered(noFilter)).toBe(false)
    expect(isFiltered({ forge: 'ado', owner: '', repos: [] })).toBe(true)
    expect(isFiltered({ forge: '', owner: 'o', repos: [] })).toBe(true)
    expect(isFiltered({ forge: '', owner: '', repos: ['github:o/a'] })).toBe(true)
  })
})

describe('selection', () => {
  beforeEach(() => clearFilter())

  it('setForge resets the owner and drops repos of other forges', () => {
    repoFilter.set({ forge: 'github', owner: 'o', repos: ['github:o/a', 'ado:p/b'] })
    setForge('ado')
    expect(get(repoFilter)).toEqual({ forge: 'ado', owner: '', repos: ['ado:p/b'] })
  })

  it('setForge back to All keeps every selected repo', () => {
    repoFilter.set({ forge: 'github', owner: 'o', repos: ['github:o/a', 'ado:p/b'] })
    setForge('')
    expect(get(repoFilter)).toEqual({ forge: '', owner: '', repos: ['github:o/a', 'ado:p/b'] })
  })

  it('setOwner drops repos of other owners', () => {
    repoFilter.set({ forge: 'ado', owner: '', repos: ['ado:p/b', 'ado:q/c'] })
    setOwner('p')
    expect(get(repoFilter)).toEqual({ forge: 'ado', owner: 'p', repos: ['ado:p/b'] })
  })

  it('toggleRepo adds then removes one key', () => {
    toggleRepo('github:o/a')
    expect(get(repoFilter).repos).toEqual(['github:o/a'])
    toggleRepo('github:o/a')
    expect(get(repoFilter).repos).toEqual([])
  })

  it('setRepos replaces the selection and clearFilter empties all three', () => {
    setRepos(['github:o/a', 'github:o/b'])
    expect(get(repoFilter).repos).toEqual(['github:o/a', 'github:o/b'])
    setForge('github')
    setOwner('o')
    clearFilter()
    expect(get(repoFilter)).toEqual(noFilter)
  })
})

describe('query round-trip', () => {
  it('parses forge, owner and every repo', () => {
    expect(parseQuery('?forge=github&owner=o&repo=github:o/a&repo=github:o/b')).toEqual({
      forge: 'github', owner: 'o', repos: ['github:o/a', 'github:o/b'],
    })
  })

  it('reads an absent filter as the empty one', () => {
    expect(parseQuery('')).toEqual(noFilter)
  })

  it('leaves unrelated params alone and writes nothing for the empty filter', () => {
    expect(toQuery(noFilter, '?x=1')).toBe('?x=1')
    expect(toQuery(noFilter, '')).toBe('')
    expect(toQuery({ forge: 'ado', owner: '', repos: [] }, '?x=1')).toContain('x=1')
  })

  it('drops a previous filter from the query it rewrites', () => {
    expect(toQuery(noFilter, '?forge=github&repo=github:o/a&x=1')).toBe('?x=1')
  })

  it('round-trips an owner with spaces and parentheses', () => {
    const filter = { forge: 'ado', owner: 'Avaloq Sourcing (ASSL)', repos: ['ado:Avaloq Sourcing (ASSL)/core'] }
    expect(parseQuery(toQuery(filter, ''))).toEqual(filter)
  })
})

describe('initRepoFilter', () => {
  beforeEach(() => clearFilter())

  it('prefers the URL filter over the stored one', () => {
    const win = fakeWin({
      search: '?forge=ado',
      stored: '{"forge":"github","owner":"o","repos":["github:o/a"]}',
    })
    const stop = initRepoFilter(win)
    expect(get(repoFilter)).toEqual({ forge: 'ado', owner: '', repos: [] })
    stop()
  })

  it('falls back to the stored filter when the URL has none', () => {
    const win = fakeWin({ stored: '{"forge":"github","owner":"o","repos":["github:o/a"]}' })
    const stop = initRepoFilter(win)
    expect(get(repoFilter)).toEqual({ forge: 'github', owner: 'o', repos: ['github:o/a'] })
    stop()
  })

  it('writes every later change to the URL and to storage', () => {
    const win = fakeWin()
    const stop = initRepoFilter(win)
    setForge('github')

    expect(win.urls.at(-1)).toBe('/?forge=github')
    expect(JSON.parse(win.read('bridge.repoFilter'))).toEqual({ forge: 'github', owner: '', repos: [] })
    stop()
  })

  it('starts empty and keeps working when localStorage throws', () => {
    const win = fakeWin({ throws: true })
    const stop = initRepoFilter(win)
    expect(get(repoFilter)).toEqual(noFilter)
    expect(() => setForge('github')).not.toThrow()
    expect(get(repoFilter).forge).toBe('github')
    stop()
  })

  it('ignores stored junk', () => {
    const win = fakeWin({ stored: 'not json' })
    const stop = initRepoFilter(win)
    expect(get(repoFilter)).toEqual(noFilter)
    stop()
  })

  it('keeps a stored repo no repo list knows yet', () => {
    const win = fakeWin({ stored: '{"forge":"","owner":"","repos":["github:gone/away"]}' })
    const stop = initRepoFilter(win)
    expect(get(repoFilter).repos).toEqual(['github:gone/away'])
    stop()
  })
})
