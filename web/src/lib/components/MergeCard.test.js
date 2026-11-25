import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'

vi.mock('../stores/sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))
const authedPost = vi.fn()
vi.mock('../auth.js', () => ({ ensureToken: async () => 'tok', authedPost: (...a) => authedPost(...a) }))
const loadInbox = vi.fn()
vi.mock('../stores/inbox.js', () => ({ loadInbox: (...a) => loadInbox(...a) }))

import MergeCard from './MergeCard.svelte'

const NOW = Date.parse('2026-11-25T12:00:00Z')
const HEAD = '96fae5fd5d9aa4022a3b5a57a69b50510a563891'

const merge = (over = {}) => ({
  pr: 9,
  prUrl: 'https://github.com/freaxnx01/bridge/pull/9',
  headSha: HEAD,
  headRef: 'ai/5',
  baseRef: 'main',
  draft: false,
  mergeableState: 'clean',
  summary: 'Caps the detail column.\n\nSecond paragraph.',
  review: { verdict: 'approve', headSha: HEAD, current: true, blocking: 0, nits: 2, source: 'marker' },
  checks: { state: 'green', passed: 6, total: 6 },
  files: [
    { path: 'internal/nav/dashboard.go', additions: 30, deletions: 5 },
    { path: 'internal/nav/dashboard_test.go', additions: 12, deletions: 2 },
  ],
  filesTruncated: false,
  stale: false,
  canMerge: true,
  reason: '',
  ...over,
})

const card = (over = {}, mergeOver = {}) => ({
  repo: 'freaxnx01/bridge',
  number: 5,
  title: 'fix(nav): keep the detail column in budget',
  url: 'https://github.com/freaxnx01/bridge/issues/5',
  kind: 'merge',
  reason: 'ready to merge',
  since: '2026-11-25T10:00:00Z',
  merge: merge(mergeOver),
  ...over,
})

function show(over = {}, mergeOver = {}) {
  return render(MergeCard, { card: card(over, mergeOver), now: NOW, onopen: () => {} })
}

let calls
beforeEach(() => {
  calls = []
  loadInbox.mockReset()
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

describe('MergeCard', () => {
  it('names the issue, the PR, the checks and the verdict', () => {
    show()
    expect(screen.getByText('bridge#5 → PR #9')).toBeTruthy()
    expect(screen.getByText(/checks ✓ 6\/6/)).toBeTruthy()
    expect(screen.getByText(/review: approve/)).toBeTruthy()
  })

  it('shows Merge only when canMerge', async () => {
    show()
    expect(screen.getByRole('button', { name: 'Merge' })).toBeTruthy()
  })

  it('replaces Merge with the reason when it is blocked', () => {
    show({ reason: 'checks running' }, { canMerge: false, reason: 'checks running', checks: { state: 'running', passed: 1, total: 6 } })
    expect(screen.queryByRole('button', { name: 'Merge' })).toBeNull()
    expect(screen.getByText('checks running')).toBeTruthy()
  })

  it('links the PR', () => {
    show()
    const link = screen.getByRole('link', { name: /Open PR/ })
    expect(link.getAttribute('href')).toBe('https://github.com/freaxnx01/bridge/pull/9')
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
  })

  it('shows the first summary line and expands to the full text', async () => {
    show()
    const summary = screen.getByRole('button', { name: /Caps the detail column/ })
    expect(summary.textContent).not.toContain('Second paragraph')
    await fireEvent.click(summary)
    expect(screen.getByRole('button', { name: /Second paragraph/ })).toBeTruthy()
  })

  it('empty summary says so', () => {
    show({}, { summary: '' })
    expect(screen.getByText('no summary in PR body')).toBeTruthy()
  })

  it('totals the files and expands to the list', async () => {
    show()
    const files = screen.getByRole('button', { name: /2 files \+42 −7/ })
    await fireEvent.click(files)
    expect(screen.getByText(/internal\/nav\/dashboard\.go/)).toBeTruthy()
    expect(screen.getByText(/\+12 −2/)).toBeTruthy()
  })

  it('says when the file list is truncated', async () => {
    show({}, { filesTruncated: true })
    await fireEvent.click(screen.getByRole('button', { name: /2 files/ }))
    expect(screen.getByText(/first 100 shown/)).toBeTruthy()
  })

  it('confirm names the exact effect', async () => {
    answers({ merged: true, branchDeleted: true })
    show()
    await fireEvent.click(screen.getByRole('button', { name: 'Merge' }))
    const dlg = screen.getByRole('dialog')
    expect(dlg.textContent).toContain('Merge PR #9 into main?')
    expect(dlg.textContent).toContain('squash')
    expect(dlg.textContent).toContain('96fae5f')
    expect(dlg.textContent).toContain('Closes #5')
    expect(dlg.textContent).toContain('branch is deleted')
    expect(calls).toEqual([])
  })

  it('posts the head SHA the card showed', async () => {
    answers({ merged: true, branchDeleted: true })
    show()
    await fireEvent.click(screen.getByRole('button', { name: 'Merge' }))
    await fireEvent.click(screen.getByRole('button', { name: /Squash & merge/ }))

    expect(calls).toEqual([
      ['/api/inbox/merge', { repo: 'freaxnx01/bridge', number: 5, pr: 9, headSha: HEAD }, 'tok'],
    ])
    expect(await screen.findByText(/Merged PR #9/)).toBeTruthy()
    expect(loadInbox).toHaveBeenCalled()
  })

  it('cancelling posts nothing', async () => {
    answers({ merged: true, branchDeleted: true })
    show()
    await fireEvent.click(screen.getByRole('button', { name: 'Merge' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(calls).toEqual([])
  })

  it('reports a branch that was not deleted', async () => {
    answers({ merged: true, branchDeleted: false, branchError: 'protected' })
    show()
    await fireEvent.click(screen.getByRole('button', { name: 'Merge' }))
    await fireEvent.click(screen.getByRole('button', { name: /Squash & merge/ }))
    expect(await screen.findByText(/branch not deleted: protected/)).toBeTruthy()
  })

  it('409 shows the reason and reloads', async () => {
    fails(409, 'head moved')
    show()
    await fireEvent.click(screen.getByRole('button', { name: 'Merge' }))
    await fireEvent.click(screen.getByRole('button', { name: /Squash & merge/ }))

    expect((await screen.findByRole('alert')).textContent).toMatch(/Not merged: head moved/)
    expect(loadInbox).toHaveBeenCalled()
  })

  it.each([
    [401, 'unauthorized', /Token rejected/],
    [403, 'Resource not accessible by integration', /Resource not accessible by integration/],
    [502, 'forge call failed', /forge call failed/],
  ])('shows the failure for status %s', async (status, message, shown) => {
    fails(status, message)
    show()
    await fireEvent.click(screen.getByRole('button', { name: 'Merge' }))
    await fireEvent.click(screen.getByRole('button', { name: /Squash & merge/ }))
    expect((await screen.findByRole('alert')).textContent).toMatch(shown)
  })

  it('does not reload the inbox on an auth failure', async () => {
    fails(401, 'unauthorized')
    show()
    await fireEvent.click(screen.getByRole('button', { name: 'Merge' }))
    await fireEvent.click(screen.getByRole('button', { name: /Squash & merge/ }))
    await screen.findByRole('alert')
    expect(loadInbox).not.toHaveBeenCalled()
  })

  it('opens the issue detail from the title, but not from the PR link', async () => {
    const onopen = vi.fn()
    render(MergeCard, { card: card(), now: NOW, onopen })

    await fireEvent.click(screen.getByRole('button', { name: /keep the detail column/ }))
    expect(onopen).toHaveBeenCalledTimes(1)

    await fireEvent.click(screen.getByRole('link', { name: /Open PR/ }))
    expect(onopen).toHaveBeenCalledTimes(1)
  })

  it('renders a title containing markup as literal text', () => {
    show({ title: 'fix: <b>x</b>' })
    expect(screen.getByText(/fix: <b>x<\/b>/)).toBeTruthy()
    expect(document.querySelector('.merge-card b')).toBeNull()
  })
})
