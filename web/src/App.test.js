import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import { writable } from 'svelte/store'

vi.mock('./lib/stores/pipeline.js', async () => {
  const { writable } = await import('svelte/store')
  return {
    pipeline: writable(null),
    pipelineError: writable(null),
    loadPipeline: vi.fn(),
    startPipelinePolling: vi.fn(() => () => {}),
    refreshPipeline: vi.fn(),
  }
})

// Without this mock, importing the real store builds an EventSource, which
// jsdom does not provide.
const inbox = writable(null)
vi.mock('./lib/stores/inbox.js', () => ({
  inbox,
  inboxError: writable(null),
  loadInbox: vi.fn(),
  startInboxPolling: vi.fn(() => () => {}),
  loadIssueDetail: vi.fn(),
}))

describe('App', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    inbox.set(null)
  })

  it('lands on the Inbox view', async () => {
    const { default: App } = await import('./App.svelte')
    render(App)
    expect(screen.getByText('Nothing needs you.')).toBeTruthy()
    expect(screen.queryByText(/RUNNING NOW/)).toBeNull()
  })

  it('reaches the Pipeline view via the nav', async () => {
    const { default: App } = await import('./App.svelte')
    render(App)
    await fireEvent.click(screen.getByRole('button', { name: 'Pipeline' }))
    expect(screen.getByText(/RUNNING NOW/)).toBeTruthy()
  })

  it('shows the inbox card count in the header', async () => {
    const card = (number) => ({ repo: 'o/r', number, title: 't', url: 'u', kind: 'human', reason: 'bug', since: '2026-11-11T10:00:00Z' })
    inbox.set({ cards: [card(1), card(2)], waiting: [], parked: [] })
    const { default: App } = await import('./App.svelte')
    render(App)
    expect(document.querySelector('.badge').textContent).toBe('2')
  })

  it('keeps the Architecture section reachable via the nav with the inlined SVG', async () => {
    const { default: App } = await import('./App.svelte')
    render(App)
    await fireEvent.click(screen.getByRole('button', { name: 'Arch' }))

    const heading = screen.getByRole('heading', { name: 'Architecture' })
    expect(heading.closest('section').querySelector('svg')).not.toBeNull()
  })
})
