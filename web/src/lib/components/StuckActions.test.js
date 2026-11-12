import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
vi.mock('../stores/sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))
const authedPost = vi.fn()
vi.mock('../auth.js', () => ({ ensureToken: async () => 'tok', authedPost: (...a) => authedPost(...a) }))

import StuckActions from './StuckActions.svelte'

const card = { kind: 'enrichment', repo: 'o/r', issue: 612, title: 't', since: '2026-10-22T00:00:00Z' }

// Each test records its own calls instead of resetting the shared mock in a
// beforeEach: clearing mock state there makes vitest report a rejection the
// component does catch as an unhandled one.
let calls
beforeEach(() => {
  calls = []
})

function answers(result) {
  authedPost.mockImplementation(async (...args) => {
    calls.push(args)
    return result
  })
}

function fails(status, message) {
  authedPost.mockImplementation(async (...args) => {
    calls.push(args)
    throw Object.assign(new Error(message), { status })
  })
}

describe('StuckActions', () => {
  it('confirms before posting, then posts clear-lock with repo and number', async () => {
    answers({ cleared: true })
    render(StuckActions, { card })
    await fireEvent.click(screen.getByRole('button', { name: 'Clear lock' }))
    expect(calls).toEqual([])
    await fireEvent.click(screen.getByRole('button', { name: /Clear lock ▶/ }))
    expect(calls).toEqual([['/api/pipeline/clear-lock', { repo: 'o/r', number: 612 }, 'tok']])
    expect(await screen.findByText(/Lock cleared/)).toBeTruthy()
  })

  it('posts clear-reenrich and reports the started session', async () => {
    answers({ slot: 'enrich-612', alreadyRunning: false })
    render(StuckActions, { card })
    await fireEvent.click(screen.getByRole('button', { name: 'Clear & re-enrich' }))
    await fireEvent.click(screen.getByRole('button', { name: /Clear & re-enrich ▶/ }))
    expect(calls).toEqual([['/api/pipeline/clear-reenrich', { repo: 'o/r', number: 612 }, 'tok']])
    expect(await screen.findByText(/enrich-612/)).toBeTruthy()
  })

  it('cancelling posts nothing', async () => {
    answers({ cleared: true })
    render(StuckActions, { card })
    await fireEvent.click(screen.getByRole('button', { name: 'Clear lock' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(calls).toEqual([])
  })

  it.each([
    [409, 'not stuck', /not stuck/],
    [422, 'lock clearing is not supported for this forge', /not supported/],
    [502, 'forge call failed', /forge call failed/],
  ])('shows the failure for status %s', async (status, message, shown) => {
    fails(status, message)
    render(StuckActions, { card })
    await fireEvent.click(screen.getByRole('button', { name: 'Clear lock' }))
    await fireEvent.click(screen.getByRole('button', { name: /Clear lock ▶/ }))
    expect((await screen.findByRole('alert')).textContent).toMatch(shown)
  })
})
