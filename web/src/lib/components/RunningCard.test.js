import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/svelte'
import RunningCard from './RunningCard.svelte'

const base = { kind: 'enrichment', repo: 'o/r', issue: 5, title: 't', status: 'enriching', since: '2026-10-26T11:00:00Z' }

describe('RunningCard enrichment link', () => {
  it('uses card.url when set', () => {
    render(RunningCard, { card: { ...base, url: 'https://git.example/o/r/issues/5' }, now: Date.now() })
    expect(screen.getByRole('link').getAttribute('href')).toBe('https://git.example/o/r/issues/5')
  })

  it('falls back to a github.com URL without card.url', () => {
    render(RunningCard, { card: base, now: Date.now() })
    expect(screen.getByRole('link').getAttribute('href')).toBe('https://github.com/o/r/issues/5')
  })
})
