import { describe, it, expect } from 'vitest'
import { requestToken, tokenPrompt } from './tokenPrompt.js'

describe('requestToken', () => {
  it('resolves a still-pending request with null when a new one replaces it', async () => {
    tokenPrompt.set(null)
    const first = requestToken()
    const second = requestToken()
    expect(await first).toBeNull()
    let settled = false
    second.then(() => { settled = true })
    await Promise.resolve()
    expect(settled).toBe(false)
  })
})
