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

describe('RunningCard pipeline duration', () => {
  const now = Date.parse('2026-11-03T12:00:00Z')
  const run = (min) => ({ kind: 'pipeline', repo: 'o/r', issue: 7, title: 'ai-implement', url: 'u', status: 'in_progress', since: new Date(now - min * 60000).toISOString() })

  it('shows the runtime in minutes without a warning color', () => {
    render(RunningCard, { card: run(12), now })
    const d = screen.getByText('12 min')
    expect(d.classList.contains('warn')).toBe(false)
    expect(d.classList.contains('over')).toBe(false)
  })

  it('colors the runtime orange past the warn threshold', () => {
    render(RunningCard, { card: run(45), now })
    expect(screen.getByText('45 min').classList.contains('warn')).toBe(true)
  })

  it('colors the runtime red past the over threshold', () => {
    render(RunningCard, { card: run(95), now })
    expect(screen.getByText('95 min').classList.contains('over')).toBe(true)
  })
})
