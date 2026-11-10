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
