import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'

import InboxCard from './InboxCard.svelte'

const NOW = Date.parse('2026-11-11T12:00:00Z')

const card = (over = {}) => ({
  repo: 'freaxnx01/bridge',
  number: 412,
  title: 'fix(nav): keep the rail in view',
  url: 'https://github.com/freaxnx01/bridge/issues/412',
  kind: 'human',
  reason: 'review blocked',
  since: '2026-11-11T10:00:00Z',
  ...over,
})

const run = (over = {}) => ({
  agent: 'claude', model: 'opus-5', costUsd: 3.1, attempt: 2, pr: 0, runUrl: '', ...over,
})

describe('InboxCard', () => {
  it('shows the human glyph, the short ref, the reason and the run meta', () => {
    render(InboxCard, { card: card({ run: run() }), now: NOW, onopen: () => {} })

    expect(screen.getByText('●')).toBeTruthy()
    expect(screen.getByText('bridge#412')).toBeTruthy()
    expect(screen.getByText('review blocked').tagName).toBe('STRONG')
    expect(screen.getByText('claude · opus-5 · $3.10 · #2')).toBeTruthy()
  })

  it('shows the retry glyph on a retry card', () => {
    render(InboxCard, { card: card({ kind: 'retry' }), now: NOW, onopen: () => {} })
    expect(screen.getByText('↻')).toBeTruthy()
  })

  it('links the PR and the run when the record has them', () => {
    render(InboxCard, {
      card: card({ run: run({ pr: 415, runUrl: 'https://github.com/o/r/actions/runs/9' }) }),
      now: NOW,
      onopen: () => {},
    })

    const pr = screen.getByRole('link', { name: /PR/ })
    expect(pr.getAttribute('href')).toBe('https://github.com/freaxnx01/bridge/pull/415')
    expect(screen.getByRole('link', { name: /Run/ }).getAttribute('href')).toBe(
      'https://github.com/o/r/actions/runs/9',
    )
    for (const link of screen.getAllByRole('link')) {
      expect(link.getAttribute('target')).toBe('_blank')
      expect(link.getAttribute('rel')).toBe('noopener noreferrer')
    }
  })

  it('shows only the issue link without a run', () => {
    render(InboxCard, { card: card(), now: NOW, onopen: () => {} })

    const links = screen.getAllByRole('link')
    expect(links).toHaveLength(1)
    expect(links[0].textContent).toContain('Issue')
  })

  it('opens the detail from the card button, but not from a link', async () => {
    const onopen = vi.fn()
    render(InboxCard, { card: card(), now: NOW, onopen })

    await fireEvent.click(screen.getByRole('button'))
    expect(onopen).toHaveBeenCalledTimes(1)

    await fireEvent.click(screen.getByRole('link', { name: /Issue/ }))
    expect(onopen).toHaveBeenCalledTimes(1)
  })

  it('renders a title containing markup as literal text', () => {
    render(InboxCard, { card: card({ title: 'fix: <b>x</b>' }), now: NOW, onopen: () => {} })
    expect(screen.getByText('fix: <b>x</b>')).toBeTruthy()
    expect(document.querySelector('.card b')).toBeNull()
  })
})
