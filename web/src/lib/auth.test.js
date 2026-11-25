import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { authedGet } from './auth.js'
import { getToken, setToken } from './token.js'
import { tokenPrompt } from './stores/tokenPrompt.js'
import { get } from 'svelte/store'

describe('authedGet', () => {
  beforeEach(() => {
    localStorage.clear()
    tokenPrompt.set(null)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function answers(status, body) {
    const fetchMock = vi.fn(async () => ({
      ok: status < 400,
      status,
      statusText: 'status ' + status,
      json: async () => body,
    }))
    vi.stubGlobal('fetch', fetchMock)
    return fetchMock
  }

  it('sends the bearer token and returns the JSON', async () => {
    const fetchMock = answers(200, { pr: 9, files: [] })
    const got = await authedGet('/api/inbox/diff?repo=o%2Fa&number=5', 'tok')
    expect(got.pr).toBe(9)
    const [path, init] = fetchMock.mock.calls[0]
    expect(path).toBe('/api/inbox/diff?repo=o%2Fa&number=5')
    expect(init.headers.Authorization).toBe('Bearer tok')
  })

  it('401 clears token', async () => {
    setToken('stale')
    answers(401, { error: 'unauthorized' })
    await expect(authedGet('/api/inbox/diff?repo=o%2Fa&number=5', 'stale')).rejects.toMatchObject({
      status: 401,
    })
    expect(getToken()).toBeNull()
    expect(get(tokenPrompt)?.rejected).toBe(true)
  })

  it('409 rethrows the message without touching the token', async () => {
    setToken('tok')
    answers(409, { error: 'not on the inbox' })
    await expect(authedGet('/api/inbox/diff?repo=o%2Fa&number=5', 'tok')).rejects.toMatchObject({
      status: 409,
      message: 'not on the inbox',
    })
    expect(getToken()).toBe('tok')
    expect(get(tokenPrompt)).toBeNull()
  })
})
