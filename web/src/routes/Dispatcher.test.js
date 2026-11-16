import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'

vi.mock('../lib/stores/sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))

import Dispatcher from './Dispatcher.svelte'

const tickAt = (h) => new Date(2026, 10, 12, h, 0).toISOString()

const history = (over = {}) => ({
  summary: [
    { repo: 'bridge', issue: 387, title: 'feat(cockpit): verdicts', evaluatedTicks: 14, pickedTicks: 3, topReason: 'type: bug vs feat' },
    { repo: 'quotes', issue: 12, title: 'fix: x', evaluatedTicks: 14, pickedTicks: 0, topReason: 'budget-exhausted 418.27/9.60 USD' },
  ],
  ticks: [
    {
      at: tickAt(18), mode: 'dry-run', trigger: 'timer', outcome: 'decided',
      pickedCount: 1, skippedCount: 1,
      candidates: [{ repo: 'bridge', issue: 387, dispatch: true }, { repo: 'quotes', issue: 12, dispatch: false }],
    },
    {
      at: tickAt(19), mode: 'dry-run', trigger: 'timer', outcome: 'decided',
      pickedCount: 0, skippedCount: 1,
      candidates: [{ repo: 'quotes', issue: 12, dispatch: false }],
    },
    {
      at: tickAt(20), mode: 'live', trigger: 'manual', outcome: 'decided',
      pickedCount: 1, skippedCount: 0,
      candidates: [{ repo: 'bridge', issue: 387, dispatch: true }],
    },
  ],
  ...over,
})

const tickRecord = () => ({
  v: 1, at: tickAt(18), trigger: 'timer', mode: 'dry-run', outcome: 'decided',
  gates: {
    at: tickAt(18), windowOpen: true, rungOn: false, usedUsd: 0, allowedUsd: 9.6, budgetKnown: true,
    nightCount: 1, nightCap: 5, globalOpen: 2, globalCap: 3, paused: false, lanes: [],
  },
  decisions: [
    {
      repo: 'bridge', issue: 387, title: 'feat(cockpit): verdicts', lane: 'auto',
      decision: 'would_dispatch', rank: 1, rank_reason: 'type: bug vs feat',
    },
    {
      repo: 'quotes', issue: 12, title: 'fix: x', lane: 'hitl', lane_reason: 'auto→hitl: no ai-merge',
      decision: 'skipped', reason: 'repo at WIP 1/1', rank: 2,
    },
  ],
})

// requests records every URL the component asked for, in order.
let requests

function stubFetch(routes) {
  requests = []
  globalThis.fetch = vi.fn(async (url) => {
    requests.push(url)
    for (const [prefix, body] of routes) {
      if (url.startsWith(prefix)) return { ok: true, json: async () => body }
    }
    return { ok: false, status: 404, statusText: 'Not Found' }
  })
}

describe('Dispatcher area', () => {
  beforeEach(() => {
    stubFetch([
      ['/api/dispatch/history', history()],
      ['/api/dispatch/ticks/', tickRecord()],
    ])
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('loads a week by default', async () => {
    render(Dispatcher)
    await waitFor(() => expect(requests[0]).toBe('/api/dispatch/history?days=7'))
  })

  it('reloads when the period changes', async () => {
    render(Dispatcher)
    await screen.findByText('#387')
    await fireEvent.click(screen.getByRole('button', { name: '30d' }))
    await waitFor(() => expect(requests).toContain('/api/dispatch/history?days=30'))
  })

  it('lists the summary rows in API order with picked counts and the typical why', async () => {
    render(Dispatcher)
    await screen.findByText('#387')
    expect(screen.getByText('3 / 14')).toBeTruthy()
    expect(screen.getByText('0 / 14')).toBeTruthy()
    expect(screen.getByText('type: bug vs feat')).toBeTruthy()
    const rows = document.querySelectorAll('.summary tbody tr')
    expect(rows[0].textContent).toContain('#387')
    expect(rows[1].textContent).toContain('#12')
  })

  it('renders one timeline chip per tick, dimming the ones that picked nothing', async () => {
    render(Dispatcher)
    await screen.findByText('#387')
    const chips = document.querySelectorAll('.timeline .chip')
    expect(chips.length).toBe(3)
    expect(chips[1].classList.contains('idle')).toBe(true)
    expect(chips[0].classList.contains('idle')).toBe(false)
  })

  it('marks manual ticks, newest tick first', async () => {
    render(Dispatcher)
    await screen.findByText('#387')
    const chips = document.querySelectorAll('.timeline .chip')
    expect(chips[0].textContent).toContain('20:00')
    expect(chips[0].textContent).toContain('manual')
    expect(chips[2].textContent).not.toContain('manual')
  })

  it('filters the timeline to one issue and opens its latest tick', async () => {
    render(Dispatcher)
    await screen.findByText('#387')

    await fireEvent.click(screen.getByText('#12'))

    await waitFor(() => expect(document.querySelectorAll('.timeline .chip').length).toBe(2))
    expect(requests).toContain(`/api/dispatch/ticks/${encodeURIComponent(tickAt(19))}`)
  })

  it('shows the picked verdict, the why and the tick gates in the detail', async () => {
    render(Dispatcher)
    await screen.findByText('#387')

    await fireEvent.click(document.querySelectorAll('.timeline .chip')[0])

    await screen.findByText('✓ would pick')
    expect(screen.getByText('✗ skip')).toBeTruthy()
    expect(screen.getByText('repo at WIP 1/1 · auto→hitl: no ai-merge')).toBeTruthy()
    expect(screen.getAllByLabelText('Dispatcher gates').length).toBeGreaterThan(0)
  })

  it('says so when nothing has been recorded yet', async () => {
    stubFetch([['/api/dispatch/history', { summary: [], ticks: [] }]])
    render(Dispatcher)
    await screen.findByText('No dispatch ticks recorded yet.')
  })

  it('reports a failed load', async () => {
    stubFetch([])
    render(Dispatcher)
    await screen.findByRole('alert')
  })
})
