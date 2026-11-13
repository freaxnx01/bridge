import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/svelte'
vi.mock('../stores/sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))

import IssueCard from './IssueCard.svelte'

const card = (over = {}) => ({
  forge: 'github', repo: 'o/r', number: 7, title: 'feat: x', url: 'u', labels: [], ...over,
})

describe('IssueCard reason badge', () => {
  it('shows the reason in the Needs-enrichment column when set', () => {
    render(IssueCard, { card: card({ reason: 'no implementation plan' }), action: 'enrich' })
    expect(screen.getByText('no implementation plan').classList.contains('reason')).toBe(true)
  })

  it('shows no badge when the card has no reason', () => {
    render(IssueCard, { card: card(), action: 'enrich' })
    expect(document.querySelector('.reason')).toBeNull()
  })
})

describe('IssueCard dispatch verdict', () => {
  it('badges the next issue the dispatcher would pick', () => {
    render(IssueCard, { card: card({ verdict: 'next', lane: 'auto' }), action: 'dispatch' })
    expect(screen.getByText('next').classList.contains('verdict-next')).toBe(true)
  })

  it('badges a skip with its reason', () => {
    render(IssueCard, {
      card: card({ verdict: 'skip', verdictReason: 'per-repo cap 1/1' }),
      action: 'dispatch',
    })
    expect(screen.getByText('skip: per-repo cap 1/1')).toBeTruthy()
  })

  it('shows no verdict badge when the card has none', () => {
    render(IssueCard, { card: card(), action: 'dispatch' })
    expect(document.querySelector('.verdict')).toBeNull()
  })

  it('shows the lane and its downgrade reason', () => {
    render(IssueCard, {
      card: card({ verdict: 'next', lane: 'hitl', laneReason: 'auto→hitl: no ai-merge' }),
      action: 'dispatch',
    })
    expect(screen.getByText('lane hitl')).toBeTruthy()
    expect(screen.getByText('auto→hitl: no ai-merge')).toBeTruthy()
  })
})
