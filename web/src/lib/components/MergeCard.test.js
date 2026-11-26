import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'

vi.mock('../stores/sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))
const authedPost = vi.fn()
const authedGet = vi.fn()
vi.mock('../auth.js', () => ({
  ensureToken: async () => 'tok',
  authedPost: (...a) => authedPost(...a),
  authedGet: (...a) => authedGet(...a),
}))
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
let diffCalls
beforeEach(() => {
  calls = []
  diffCalls = []
  loadInbox.mockReset()
  authedGet.mockReset()
  authedGet.mockImplementation(async (...args) => {
    diffCalls.push(args)
    return diffPayload()
  })
})

// diffPayload is what GET /api/inbox/diff answers for the fixture's PR.
const diffPayload = (over = {}) => ({
  pr: 9,
  headSha: HEAD,
  truncated: false,
  files: [{ path: 'a.go', status: 'modified', additions: 1, deletions: 0,
            patch: '@@ -1 +1 @@\n+x', patchMissing: false }],
  ...over,
})

function diffFails(status, message) {
  authedGet.mockImplementation(async (...args) => {
    diffCalls.push(args)
    throw Object.assign(new Error(message), { status })
  })
}

const diffButton = () => screen.getByRole('button', { name: /diff/ })

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

  it('several closing PRs: names no PR and links none', () => {
    show({ reason: 'several open PRs close this issue' }, {
      pr: 0, prUrl: '', headSha: '', canMerge: false, reason: 'several open PRs close this issue',
      checks: undefined, review: null, files: [], summary: '',
    })
    expect(screen.queryByText(/PR #0/)).toBeNull()
    expect(screen.queryByRole('link', { name: /Open PR/ })).toBeNull()
    expect(screen.getByText(/several open PRs close this issue/)).toBeTruthy()
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

  it('posts the head the dialog showed even if the card moves while it is open', async () => {
    answers({ merged: true, branchDeleted: true })
    const NEW = '2222222222222222222222222222222222222222'
    const { rerender } = show()
    await fireEvent.click(screen.getByRole('button', { name: 'Merge' }))
    await rerender({ card: card({}, { headSha: NEW, pr: 10 }), now: NOW, onopen: () => {} })

    expect(screen.getByRole('dialog').textContent).toContain('96fae5f')
    await fireEvent.click(screen.getByRole('button', { name: /Squash & merge/ }))

    expect(calls).toEqual([
      ['/api/inbox/merge', { repo: 'freaxnx01/bridge', number: 5, pr: 9, headSha: HEAD }, 'tok'],
    ])
  })

  it('takes Merge away once the merge landed, so it cannot be posted twice', async () => {
    answers({ merged: true, branchDeleted: true })
    show()
    await fireEvent.click(screen.getByRole('button', { name: 'Merge' }))
    await fireEvent.click(screen.getByRole('button', { name: /Squash & merge/ }))
    await screen.findByText(/Merged PR #9/)

    expect(screen.queryByRole('button', { name: 'Merge' })).toBeNull()
    expect(calls.length).toBe(1)
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

  it('loads the diff on tap with the token', async () => {
    show()
    await fireEvent.click(diffButton())

    expect(diffCalls).toEqual([['/api/inbox/diff?repo=freaxnx01%2Fbridge&number=5', 'tok']])
    expect(await screen.findByRole('button', { name: /a\.go/ })).toBeTruthy()
  })

  it('loads the diff once and toggles it closed again', async () => {
    show()
    await fireEvent.click(diffButton())
    await screen.findByRole('button', { name: /a\.go/ })
    await fireEvent.click(diffButton())
    expect(screen.queryByRole('button', { name: /a\.go/ })).toBeNull()
    await fireEvent.click(diffButton())
    await screen.findByRole('button', { name: /a\.go/ })

    expect(diffCalls.length).toBe(1)
  })

  it('drops the diff when the head SHA changes', async () => {
    const NEW = '2222222222222222222222222222222222222222'
    const { rerender } = show()
    await fireEvent.click(diffButton())
    await screen.findByRole('button', { name: /a\.go/ })

    await rerender({ card: card({}, { headSha: NEW }), now: NOW, onopen: () => {} })
    expect(screen.queryByRole('button', { name: /a\.go/ })).toBeNull()

    await fireEvent.click(diffButton())
    expect(diffCalls.length).toBe(2)
  })

  it('409 shows the reason', async () => {
    diffFails(409, 'not on the inbox')
    show()
    await fireEvent.click(diffButton())
    expect((await screen.findByRole('alert')).textContent).toMatch(/Diff unavailable: not on the inbox/)
  })

  it.each([
    [401, 'unauthorized', /Token rejected/],
    [502, 'forge call failed', /forge call failed/],
  ])('diff failure for status %s', async (status, message, shown) => {
    diffFails(status, message)
    show()
    await fireEvent.click(diffButton())
    expect((await screen.findByRole('alert')).textContent).toMatch(shown)
  })

  it('renders a title containing markup as literal text', () => {
    show({ title: 'fix: <b>x</b>' })
    expect(screen.getByText(/fix: <b>x<\/b>/)).toBeTruthy()
    expect(document.querySelector('.merge-card b')).toBeNull()
  })
})
