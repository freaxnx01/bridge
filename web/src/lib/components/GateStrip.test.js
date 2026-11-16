import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/svelte'

import GateStrip from './GateStrip.svelte'

const gates = (over = {}) => ({
  at: new Date(2026, 10, 12, 14, 0).toISOString(),
  windowOpen: true,
  rungOn: true,
  usedUsd: 2,
  allowedUsd: 9.6,
  budgetKnown: true,
  nightCount: 0,
  nightCap: 5,
  globalOpen: 0,
  globalCap: 3,
  paused: false,
  lanes: [],
  ...over,
})

describe('GateStrip', () => {
  it('renders nothing without gates', () => {
    const { container } = render(GateStrip, { gates: undefined })
    expect(container.textContent.trim()).toBe('')
  })

  it('names the next opening when the window is closed', () => {
    render(GateStrip, {
      gates: gates({ windowOpen: false, nextOpening: new Date(2026, 10, 12, 18, 0).toISOString() }),
    })
    expect(screen.getByText('window closed · opens 18:00')).toBeTruthy()
  })

  it('says the window is open while it is', () => {
    render(GateStrip, { gates: gates() })
    expect(screen.getByText('window open')).toBeTruthy()
  })

  it('warns when the budget rung is over its cap', () => {
    render(GateStrip, { gates: gates({ usedUsd: 418.27 }) })
    const budget = screen.getByText('budget 4357%')
    expect(budget.classList.contains('warn')).toBe(true)
  })

  it('does not warn inside the budget', () => {
    render(GateStrip, { gates: gates({ usedUsd: 2 }) })
    expect(screen.getByText('budget 21%').classList.contains('warn')).toBe(false)
  })

  it('says so when usage could not be measured', () => {
    render(GateStrip, { gates: gates({ budgetKnown: false }) })
    expect(screen.getByText('budget unreadable').classList.contains('warn')).toBe(true)
  })

  it('says so when the rung is off', () => {
    render(GateStrip, { gates: gates({ rungOn: false }) })
    expect(screen.getByText('budget rung off')).toBeTruthy()
  })

  it('shows the nightly and global counters', () => {
    render(GateStrip, { gates: gates({ nightCount: 2, globalOpen: 1 }) })
    expect(screen.getByText('night 2/5')).toBeTruthy()
    expect(screen.getByText('PRs 1/3')).toBeTruthy()
  })

  it('shows paused only when paused', () => {
    render(GateStrip, { gates: gates({ paused: true }) })
    expect(screen.getByText('paused')).toBeTruthy()
  })

  it('shows each lane with its mode and spent counter', () => {
    render(GateStrip, { gates: gates({ lanes: [{ name: 'auto', dryRun: true, dispatched: 3, max: 12 }] }) })
    expect(screen.getByText('auto: dry-run 3/12')).toBeTruthy()
  })

  it('renders an unbounded lane ceiling as ∞', () => {
    render(GateStrip, { gates: gates({ lanes: [{ name: 'hitl', dryRun: false, dispatched: 1, max: 0 }] }) })
    expect(screen.getByText('hitl: live 1/∞')).toBeTruthy()
  })

  it('shows the last tick when there was one', () => {
    render(GateStrip, { gates: gates({ lastTick: new Date(2026, 10, 12, 13, 30).toISOString() }) })
    expect(screen.getByText('last tick 13:30')).toBeTruthy()
  })
})
