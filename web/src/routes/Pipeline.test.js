import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { get } from 'svelte/store'

vi.mock('../lib/stores/sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))

import Pipeline from './Pipeline.svelte'
import { pipeline, pipelineError } from '../lib/stores/pipeline.js'
import { tokenPrompt } from '../lib/stores/tokenPrompt.js'

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
    stubFetch((url) => (url === '/api/pipeline' ? { status: 200, body: board() } : { status: 202, body: {} }))
  })
  afterEach(() => vi.unstubAllGlobals())

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
})
