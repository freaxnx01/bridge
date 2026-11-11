import { describe, it, expect } from 'vitest'
import { shortRef, prUrl, runMeta } from './inbox.js'

describe('shortRef', () => {
  it('shows the repo name after the slash with the issue number', () => {
    expect(shortRef({ repo: 'freaxnx01/bridge', number: 412 })).toBe('bridge#412')
  })
})

describe('prUrl', () => {
  it('builds the GitHub pull request URL', () => {
    expect(prUrl('o/r', 7)).toBe('https://github.com/o/r/pull/7')
  })
})

describe('runMeta', () => {
  it('joins agent, model, cost and attempt', () => {
    expect(runMeta({ agent: 'claude', model: 'opus-5', costUsd: 3.1, attempt: 2 })).toBe(
      'claude · opus-5 · $3.10 · #2',
    )
  })

  it('drops the attempt when it is absent', () => {
    expect(runMeta({ agent: 'claude', model: 'opus-5', costUsd: 3.1, attempt: 0 })).not.toContain('#')
  })

  it('drops empty parts', () => {
    expect(runMeta({ agent: '', model: 'opus-5', costUsd: 0, attempt: 0 })).toBe('opus-5 · $0.00')
  })

  it('is empty without a run', () => {
    expect(runMeta(null)).toBe('')
  })
})
