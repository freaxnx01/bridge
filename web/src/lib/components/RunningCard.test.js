import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/svelte'
vi.mock('../stores/sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))

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

describe('RunningCard stuck actions', () => {
  const now = Date.parse('2026-11-11T12:00:00Z')
  const since = (s) => new Date(now - s * 1000).toISOString()

  it('offers both actions on an enrichment older than 6 h', () => {
    render(RunningCard, { card: { ...base, since: since(7 * 3600) }, now })
    expect(screen.getByRole('button', { name: 'Clear lock' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Clear & re-enrich' })).toBeTruthy()
  })

  it('offers none on a fresh enrichment or a pipeline run', () => {
    render(RunningCard, { card: { ...base, since: since(600) }, now })
    expect(screen.queryByRole('button')).toBeNull()
  })
})
