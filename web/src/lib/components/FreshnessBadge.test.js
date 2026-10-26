import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/svelte'
import FreshnessBadge from './FreshnessBadge.svelte'

const now = Date.parse('2026-10-26T12:00:00Z')
const ago = (s) => new Date(now - s * 1000).toISOString()

describe('FreshnessBadge', () => {
  it('warns when collectedAt is older than 2x interval', () => {
    render(FreshnessBadge, { collectedAt: ago(130), intervalSeconds: 60, now })
    const el = screen.getByTestId('freshness')
    expect(el.classList.contains('stale')).toBe(true)
    expect(el.textContent).toContain('⚠')
    expect(el.textContent).toContain('2m')
  })

  it('shows age without warning when fresh', () => {
    render(FreshnessBadge, { collectedAt: ago(12), intervalSeconds: 60, now })
    const el = screen.getByTestId('freshness')
    expect(el.classList.contains('stale')).toBe(false)
    expect(el.textContent).not.toContain('⚠')
    expect(el.textContent).toContain('12s')
  })
})
