import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'

vi.mock('../stores/sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))

import IssueDetail from './IssueDetail.svelte'

const run = (over = {}) => ({
  attempt: 0, class: 'rate_limit', decision: 'retry', status: '', agent: 'claude', model: 'opus-5',
  turns: 12, costUsd: 0, pr: 0, runUrl: '', posted: '2026-11-11T08:00:00Z', ...over,
})

function stubDetail(body) {
  const f = vi.fn(async () => ({ ok: true, status: 200, statusText: 'OK', json: async () => body }))
  vi.stubGlobal('fetch', f)
  return f
}

const detail = (runs) => ({
  issue: { repo: 'freaxnx01/bridge', number: 412, title: 'fix(nav): x', url: 'u', labels: ['ai:failed'] },
  runs,
})

describe('IssueDetail', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(() => vi.unstubAllGlobals())

  it('renders runs in the order the API returned them', async () => {
    stubDetail(detail([
      run({ attempt: 2, class: 'task_failure', decision: 'stop', costUsd: 3.1 }),
      run({ attempt: 1 }),
    ]))
    render(IssueDetail, { repo: 'freaxnx01/bridge', number: 412, onclose: () => {} })

    const rows = await waitFor(() => {
      const found = document.querySelectorAll('.run')
      expect(found).toHaveLength(2)
      return found
    })
    expect(rows[0].textContent).toContain('task_failure')
    expect(rows[0].textContent).toContain('stop')
    expect(rows[0].textContent).toContain('$3.10')
    expect(rows[0].textContent).toContain('#2')
    expect(rows[1].textContent).toContain('rate_limit')
  })

  it('links the PR and the run only when the record has them', async () => {
    stubDetail(detail([
      run({ pr: 415, runUrl: 'https://github.com/o/r/actions/runs/9' }),
      run({ pr: 0, runUrl: '' }),
    ]))
    render(IssueDetail, { repo: 'freaxnx01/bridge', number: 412, onclose: () => {} })

    await waitFor(() => expect(document.querySelectorAll('.run')).toHaveLength(2))
    const rows = document.querySelectorAll('.run')
    expect(rows[0].querySelector('a[href*="/pull/415"]')).not.toBeNull()
    expect(rows[0].querySelector('a[href*="/pull/415"]').getAttribute('href')).toBe(
      'https://github.com/freaxnx01/bridge/pull/415',
    )
    expect(rows[0].querySelectorAll('a')).toHaveLength(2)
    expect(rows[1].querySelectorAll('a')).toHaveLength(0)
  })

  it('says so when there are no run reports', async () => {
    stubDetail(detail([]))
    render(IssueDetail, { repo: 'o/r', number: 1, onclose: () => {} })
    expect(await screen.findByText('No run reports.')).toBeTruthy()
  })

  it('shows an error when the fetch fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false, status: 502, statusText: 'Bad Gateway' })))
    render(IssueDetail, { repo: 'o/r', number: 1, onclose: () => {} })
    expect(await screen.findByText('Could not load issue.')).toBeTruthy()
  })

  it('closes on Escape and on the back button', async () => {
    stubDetail(detail([]))
    const onclose = vi.fn()
    render(IssueDetail, { repo: 'freaxnx01/bridge', number: 412, onclose })
    await screen.findByText('No run reports.')

    await fireEvent.keyDown(window, { key: 'Escape' })
    expect(onclose).toHaveBeenCalledTimes(1)

    await fireEvent.click(screen.getByRole('button', { name: 'Back' }))
    expect(onclose).toHaveBeenCalledTimes(2)
  })

  it('reloads when it is pointed at another issue', async () => {
    const f = vi.fn(async (path) => ({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: async () =>
        path.endsWith('/412')
          ? detail([run({ class: 'task_failure' })])
          : { issue: { repo: 'freaxnx01/bridge', number: 500, title: 'other', url: 'u', labels: [] }, runs: [] },
    }))
    vi.stubGlobal('fetch', f)
    const { rerender } = render(IssueDetail, { repo: 'freaxnx01/bridge', number: 412, onclose: () => {} })
    await waitFor(() => expect(document.querySelectorAll('.run')).toHaveLength(1))

    await rerender({ repo: 'freaxnx01/bridge', number: 500, onclose: () => {} })

    expect(await screen.findByText('No run reports.')).toBeTruthy()
    expect(screen.getByRole('dialog', { name: 'bridge#500' })).toBeTruthy()
    expect(screen.getByText('other')).toBeTruthy()
    expect(document.querySelectorAll('.run')).toHaveLength(0)
  })

  it('clears a failed load when it is pointed at another issue', async () => {
    const f = vi.fn(async (path) =>
      path.endsWith('/412')
        ? { ok: false, status: 502, statusText: 'Bad Gateway' }
        : { ok: true, status: 200, statusText: 'OK', json: async () => detail([]) },
    )
    vi.stubGlobal('fetch', f)
    const { rerender } = render(IssueDetail, { repo: 'freaxnx01/bridge', number: 412, onclose: () => {} })
    await screen.findByText('Could not load issue.')

    await rerender({ repo: 'freaxnx01/bridge', number: 500, onclose: () => {} })

    expect(await screen.findByText('No run reports.')).toBeTruthy()
    expect(screen.queryByText('Could not load issue.')).toBeNull()
  })

  it('names the dialog after the issue', async () => {
    stubDetail(detail([]))
    render(IssueDetail, { repo: 'freaxnx01/bridge', number: 412, onclose: () => {} })
    expect(screen.getByRole('dialog', { name: 'bridge#412' })).toBeTruthy()
  })
})
