import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { get } from 'svelte/store'

vi.mock('../lib/stores/sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))

import Pipeline from './Pipeline.svelte'
import { pipeline, pipelineError } from '../lib/stores/pipeline.js'
import { tokenPrompt } from '../lib/stores/tokenPrompt.js'
import { repoFilter, clearFilter } from '../lib/stores/repoFilter.js'

const iso = (s) => new Date(Date.now() - s * 1000).toISOString()

function board(over = {}) {
  return {
    running: [
      { kind: 'pipeline', repo: 'freaxnx01/rhyflitzer', issue: 318, title: 'ai-implement', url: 'https://github.com/x/y/actions/runs/1', status: 'in_progress', since: iso(1380) },
      { kind: 'session', repo: 'bridge', title: 'bridge', status: 'busy', since: iso(7200), sessionId: 'session_abc', cwd: '/w/bridge/.worktrees/webui' },
    ],
    ready: [
      { forge: 'github', repo: 'freaxnx01/bridge', number: 351, title: 'fix(api): thing', url: 'https://github.com/freaxnx01/bridge/issues/351', labels: ['size:S'] },
    ],
    held: [
      { forge: 'github', repo: 'freaxnx01/quotes', number: 12, title: 'docs: x', url: 'u', labels: [], reason: 'open PR' },
    ],
    needsEnrichment: [
      { forge: 'github', repo: 'freaxnx01/flowhub', number: 88, title: 'feat: y', url: 'u', labels: ['needs-enrichment'] },
      { forge: 'forgejo', repo: 'freaxnx01/game-x', number: 3, title: 'feat: z', url: 'u', labels: [] },
    ],
    dispatchPaused: false,
    collectedAt: { issues: iso(10), runs: iso(5), sessions: iso(8) },
    intervalsSeconds: { issues: 300, runs: 30, sessions: 60 },
    errors: [],
    ...over,
  }
}

function stubFetch(handler) {
  const f = vi.fn(async (url, opts) => {
    // the board reload (GET) always serves whatever the store currently holds
    const r = url === '/api/pipeline' ? { status: 200, body: get(pipeline) } : handler(url, opts)
    return { ok: r.status < 400, status: r.status, statusText: 'x', json: async () => r.body }
  })
  vi.stubGlobal('fetch', f)
  return f
}

describe('Pipeline', () => {
  beforeEach(() => {
    localStorage.clear()
    pipelineError.set(null)
    pipeline.set(board())
    tokenPrompt.set(null)
    clearFilter()
    stubFetch((url) => (url === '/api/pipeline' ? { status: 200, body: board() } : { status: 202, body: {} }))
  })

  describe('repo filter', () => {
    const issue = (repo, number, over = {}) => ({ forge: 'github', repo, number, title: `t${number}`, url: `u${number}`, labels: [], ...over })
    const twoRepos = () => board({
      running: [
        { kind: 'pipeline', forge: 'github', repo: 'o/a', title: 'ci', status: 'in_progress', since: iso(60) },
        { kind: 'pipeline', forge: 'github', repo: 'p/b', title: 'ci', status: 'in_progress', since: iso(60) },
      ],
      ready: [issue('o/a', 1), issue('p/b', 2)],
      held: [issue('o/a', 3, { reason: 'open PR' }), issue('p/b', 4, { reason: 'open PR' })],
      needsEnrichment: [issue('o/a', 5), issue('p/b', 6)],
    })

    it('keeps only the selected repos items in every column', async () => {
      pipeline.set(twoRepos())
      repoFilter.set({ forge: '', owner: '', repos: ['github:o/a'] })
      render(Pipeline)

      expect(screen.getByText(/RUNNING NOW · 1/)).toBeTruthy()
      expect(screen.getByText(/READY TO DISPATCH · 1/)).toBeTruthy()
      expect(screen.getByText(/NEEDS ENRICHMENT · 1/)).toBeTruthy()
      expect(screen.getByText(/#1\b/)).toBeTruthy()
      expect(screen.queryByText(/#2\b/)).toBeNull()
      expect(screen.queryByText(/#6\b/)).toBeNull()

      await fireEvent.click(screen.getByRole('button', { name: /Held · 1/ }))
      expect(screen.getByText(/#3\b/)).toBeTruthy()
      expect(screen.queryByText(/#4\b/)).toBeNull()
    })

    it('counts the hidden needs-enrichment cards after filtering', () => {
      const many = [...Array(12)].map((_, i) => issue('o/a', 100 + i))
      pipeline.set(board({ needsEnrichment: [...many, issue('p/b', 200), issue('p/b', 201), issue('p/b', 202)] }))
      repoFilter.set({ forge: 'github', owner: 'o', repos: [] })
      render(Pipeline)

      expect(screen.getByRole('button', { name: 'show 2 more' })).toBeTruthy()
    })

    it('offers to clear the filter when it hides everything', async () => {
      pipeline.set(twoRepos())
      repoFilter.set({ forge: '', owner: '', repos: ['github:gone/away'] })
      render(Pipeline)

      expect(screen.getByText('Nothing for the selected repos.')).toBeTruthy()
      expect(screen.queryByText(/RUNNING NOW/)).toBeNull()

      await fireEvent.click(screen.getByRole('button', { name: '✕ clear filter' }))
      expect(get(repoFilter)).toEqual({ forge: '', owner: '', repos: [] })
    })

    it('shows the columns loading, not the filter empty state, before the board arrives', () => {
      pipeline.set(null)
      repoFilter.set({ forge: '', owner: '', repos: ['github:o/a'] })
      render(Pipeline)

      expect(screen.queryByText('Nothing for the selected repos.')).toBeNull()
      expect(screen.getByText(/RUNNING NOW · …/)).toBeTruthy()
    })

    it('keeps the load error and its Retry visible under an active filter', () => {
      pipeline.set(null)
      pipelineError.set('boom')
      repoFilter.set({ forge: '', owner: '', repos: ['github:o/a'] })
      render(Pipeline)

      expect(screen.queryByText('Nothing for the selected repos.')).toBeNull()
      expect(screen.getAllByText(/Couldn't load: boom/).length).toBe(3)
      expect(screen.getAllByRole('button', { name: 'Retry' }).length).toBe(3)
    })
  })
  afterEach(() => vi.unstubAllGlobals())

  it('labels the Held toggle with the real hold reasons', () => {
    render(Pipeline)
    expect(screen.getByText('(milestone, open PR, attempts)')).toBeTruthy()
  })

  it('renders the three columns from the store', () => {
    render(Pipeline)
    expect(screen.getByText(/RUNNING NOW · 2/)).toBeTruthy()
    expect(screen.getByText(/READY TO DISPATCH · 1/)).toBeTruthy()
    expect(screen.getByText(/NEEDS ENRICHMENT · 2/)).toBeTruthy()
    expect(screen.getByText(/#318/)).toBeTruthy()
    expect(screen.getByText(/#351/)).toBeTruthy()
    expect(screen.getByText(/#88/)).toBeTruthy()
  })

  it('hides the Held list until expanded', async () => {
    render(Pipeline)
    expect(screen.queryByText(/#12\b/)).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: /Held · 1/ }))
    expect(screen.getByText(/#12\b/)).toBeTruthy()
    expect(screen.getByText('open PR')).toBeTruthy()
  })

  it('counts only active work and folds idle sessions and stuck labels away', async () => {
    const idle = { kind: 'session', repo: 'o/quiet', title: 'quiet-tab', status: 'idle', since: iso(86400), sessionId: 's-idle', cwd: '/w/quiet' }
    const stuck = { kind: 'enrichment', repo: 'o/r', issue: 253, title: 't', url: 'https://github.com/o/r/issues/253', status: 'enriching', since: iso(54 * 86400) }
    pipeline.set(board({ running: [...board().running, idle, stuck] }))
    render(Pipeline)
    expect(screen.getByText(/RUNNING NOW · 2/)).toBeTruthy()
    expect(screen.queryByText(/quiet-tab/)).toBeNull()
    expect(screen.queryByText(/#253/)).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: /Idle sessions · 1/ }))
    expect(screen.getByText(/quiet-tab/)).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: /Stuck enriching · 1/ }))
    expect(screen.getByText(/#253/)).toBeTruthy()
  })

  it('shows Nothing running with the idle toggle when every session is idle', () => {
    const idle = { kind: 'session', repo: 'o/quiet', title: 'quiet', status: 'idle', since: iso(60), sessionId: 's-idle', cwd: '/w/quiet' }
    pipeline.set(board({ running: [idle] }))
    render(Pipeline)
    expect(screen.getByText('Nothing running.')).toBeTruthy()
    expect(screen.getByRole('button', { name: /Idle sessions · 1/ })).toBeTruthy()
  })

  it('shows loading, not error, when a column source has no collectedAt yet', () => {
    pipeline.set(board({ collectedAt: { issues: iso(3) }, running: [] }))
    render(Pipeline)
    expect(screen.getByText(/RUNNING NOW · …/)).toBeTruthy()
    expect(screen.queryByText(/Couldn't load/)).toBeNull()
  })

  it('shows Couldn\'t load with Retry when there is no data and the fetch failed', async () => {
    pipeline.set(null)
    pipelineError.set('boom')
    render(Pipeline)
    expect(screen.getAllByText(/Couldn't load: boom/).length).toBe(3)
    expect(screen.getAllByRole('button', { name: 'Retry' }).length).toBe(3)
  })

  it('shows the first 10 needs-enrichment cards then show N more', async () => {
    const many = Array.from({ length: 14 }, (_, i) => ({ forge: 'github', repo: 'o/r', number: 100 + i, title: 'feat: t', url: 'u', labels: [] }))
    pipeline.set(board({ needsEnrichment: many }))
    render(Pipeline)
    expect(screen.queryByText(/#110\b/)).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'show 4 more' }))
    expect(screen.getByText(/#113\b/)).toBeTruthy()
  })

  it('shows dispatch-paused note when dispatchPaused', () => {
    pipeline.set(board({ dispatchPaused: true }))
    render(Pipeline)
    expect(screen.getByText(/dispatch paused/i)).toBeTruthy()
  })

  it('error strip dismiss hides the error until the next board update', async () => {
    pipeline.set(board({ errors: [{ source: 'issues', error: '401 Bad credentials', since: iso(60) }] }))
    render(Pipeline)
    await new Promise((r) => setTimeout(r, 0)) // let the on-mount reload settle
    expect(screen.getByText(/401 Bad credentials/)).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'dismiss' }))
    expect(screen.queryByText(/401 Bad credentials/)).toBeNull()
    pipeline.set(board({ errors: [{ source: 'issues', error: '401 Bad credentials', since: iso(60) }] }))
    await waitFor(() => expect(screen.getByText(/401 Bad credentials/)).toBeTruthy())
  })

  it('shows one muted line per skipped forge, outside the error strip', () => {
    pipeline.set(board({ skipped: [{ forge: 'ado', repos: 10 }] }))
    render(Pipeline)
    const line = screen.getByText('10 Azure DevOps repos not on the board (forge not supported).')
    expect(line.closest('.errors')).toBeNull()
    expect(screen.queryByRole('button', { name: 'dismiss' })).toBeNull()
  })

  it('says "repo" for a single skipped repo', () => {
    pipeline.set(board({ skipped: [{ forge: 'ado', repos: 1 }] }))
    render(Pipeline)
    expect(screen.getByText('1 Azure DevOps repo not on the board (forge not supported).')).toBeTruthy()
  })

  it('shows nothing for an empty or missing skipped list', () => {
    pipeline.set(board({ skipped: [] }))
    render(Pipeline)
    expect(screen.queryByText(/not on the board/)).toBeNull()
  })

  it('Dispatch asks for a token when none is stored', async () => {
    const f = stubFetch(() => ({ status: 200, body: {} }))
    render(Pipeline)
    await fireEvent.click(screen.getByRole('button', { name: 'Dispatch' }))
    await waitFor(() => expect(get(tokenPrompt)).not.toBeNull())
    expect(f.mock.calls.filter(([, o]) => o?.method === 'POST')).toHaveLength(0)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('shows already running when the action returns alreadyRunning', async () => {
    localStorage.setItem('bridge.apiToken', 't0k')
    stubFetch(() => ({ status: 200, body: { slot: 's', alreadyRunning: true } }))
    render(Pipeline)
    await fireEvent.click(screen.getByRole('button', { name: 'Dispatch' }))
    await fireEvent.click(await screen.findByRole('button', { name: /Dispatch ▶/ }))
    await waitFor(() => expect(screen.getByText(/Already running/)).toBeTruthy())
  })

  it('shows an rcUrl link when returned and sends the bearer token', async () => {
    localStorage.setItem('bridge.apiToken', 't0k')
    const f = stubFetch(() => ({ status: 200, body: { slot: 'bridge-wt-dispatch-351', alreadyRunning: false, rcUrl: 'https://claude.ai/code/session_z' } }))
    render(Pipeline)
    await fireEvent.click(screen.getByRole('button', { name: 'Dispatch' }))
    await fireEvent.click(await screen.findByRole('button', { name: /Dispatch ▶/ }))
    await screen.findByText(/Started/)
    const hrefs = screen.getAllByRole('link', { name: /Open RC/ }).map((a) => a.getAttribute('href'))
    expect(hrefs).toContain('https://claude.ai/code/session_z')
    const post = f.mock.calls.find(([u]) => u === '/api/pipeline/dispatch')
    expect(post[1].headers.Authorization).toBe('Bearer t0k')
    expect(JSON.parse(post[1].body)).toEqual({ repo: 'freaxnx01/bridge', number: 351 })
  })

  it('a 401 clears the token and reopens the token prompt as rejected', async () => {
    localStorage.setItem('bridge.apiToken', 'bad')
    stubFetch(() => ({ status: 401, body: { error: 'unauthorized' } }))
    render(Pipeline)
    await fireEvent.click(screen.getByRole('button', { name: 'Dispatch' }))
    await fireEvent.click(await screen.findByRole('button', { name: /Dispatch ▶/ }))
    await waitFor(() => expect(get(tokenPrompt)?.rejected).toBe(true))
    expect(localStorage.getItem('bridge.apiToken')).toBeNull()
  })

  it('disables Enrich for non-GitHub issues with a GH only label', () => {
    render(Pipeline)
    expect(screen.getByText('(GH only)')).toBeTruthy()
    const buttons = screen.getAllByRole('button', { name: 'Enrich' })
    expect(buttons.filter((b) => b.disabled)).toHaveLength(1)
  })

  it('Refresh button posts /api/pipeline/refresh with the token', async () => {
    localStorage.setItem('bridge.apiToken', 't0k')
    const f = stubFetch((url) => (url === '/api/pipeline' ? { status: 200, body: board() } : { status: 202, body: {} }))
    render(Pipeline)
    await fireEvent.click(screen.getAllByRole('button', { name: 'Refresh' })[0])
    await waitFor(() => {
      const post = f.mock.calls.find(([u]) => u === '/api/pipeline/refresh')
      expect(post).toBeTruthy()
      expect(post[1].headers.Authorization).toBe('Bearer t0k')
    })
  })

  it('renders two unlinked pipeline runs in the same repo without throwing', () => {
    const run = { kind: 'pipeline', repo: 'o/r', title: 'ci', status: 'queued', since: iso(5) }
    pipeline.set(board({ running: [run, { ...run }] }))
    render(Pipeline)
    expect(screen.getByText(/RUNNING NOW · 2/)).toBeTruthy()
  })

  it('keeps cards with the same repo and number on different forges apart', () => {
    const a = { forge: 'github', repo: 'o/r', number: 5, title: 'feat: a', url: 'u1', labels: [] }
    pipeline.set(board({ needsEnrichment: [a, { ...a, forge: 'forgejo', url: 'u2' }] }))
    render(Pipeline)
    expect(screen.getByText(/NEEDS ENRICHMENT · 2/)).toBeTruthy()
  })

  it.each([
    [403, /wrong origin/i],
    [503, /token not configured/i],
    [500, /refresh failed/i],
  ])('Refresh shows an inline error on %i', async (status, text) => {
    localStorage.setItem('bridge.apiToken', 't0k')
    stubFetch(() => ({ status, body: { error: 'x' } }))
    render(Pipeline)
    await fireEvent.click(screen.getAllByRole('button', { name: 'Refresh' })[0])
    expect((await screen.findAllByText(text)).length).toBeGreaterThan(0)
  })
})
