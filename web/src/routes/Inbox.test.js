import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import { writable } from 'svelte/store'

vi.mock('../lib/stores/sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))

const inbox = writable(null)
const inboxError = writable(null)

vi.mock('../lib/stores/inbox.js', () => ({
  inbox,
  inboxError,
  loadInbox: vi.fn(),
  startInboxPolling: vi.fn(() => () => {}),
  loadIssueDetail: vi.fn(async () => ({ issue: { repo: 'freaxnx01/bridge', number: 412, title: 't', labels: [] }, runs: [] })),
}))

const card = (over = {}) => ({
  repo: 'freaxnx01/bridge', number: 412, title: 'fix(nav): x', url: 'u',
  kind: 'human', reason: 'review blocked', since: '2026-11-11T10:00:00Z', ...over,
})

async function renderInbox(data, error = null) {
  inbox.set(data)
  inboxError.set(error)
  const { default: Inbox } = await import('./Inbox.svelte')
  return render(Inbox)
}

describe('Inbox', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    inbox.set(null)
    inboxError.set(null)
  })

  it('shows the empty state and still lists what is waiting', async () => {
    await renderInbox({
      cards: [],
      waiting: [card({ number: 253, kind: 'wait', reason: 'running' })],
      parked: [],
    })

    expect(screen.getByText('Nothing needs you.')).toBeTruthy()
    expect(screen.getByText('Running & waiting')).toBeTruthy()
    expect(screen.getByText('bridge#253')).toBeTruthy()
  })

  it('keeps the parked group collapsed until it is opened', async () => {
    await renderInbox({
      cards: [],
      waiting: [],
      parked: [
        card({ number: 1, kind: 'parked', reason: 'parked', title: 'parked one' }),
        card({ number: 2, kind: 'parked', reason: 'parked', title: 'parked two' }),
      ],
    })

    expect(screen.queryByText('parked one')).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: /Parked \(2\)/ }))
    expect(screen.getByText('parked one')).toBeTruthy()
  })

  it('shows a warning line per failed comment fetch', async () => {
    await renderInbox({ cards: [], waiting: [], parked: [], errors: { 'comments o/a#5': 'boom' } })
    expect(screen.getByText(/Could not read comments: comments o\/a#5/)).toBeTruthy()
  })

  it('shows the load error when there is no data', async () => {
    await renderInbox(null, '/api/inbox: 500 Internal Server Error')
    expect(screen.getByText(/500 Internal Server Error/)).toBeTruthy()
  })

  it('opens the issue detail from a card and closes it on Escape', async () => {
    await renderInbox({ cards: [card()], waiting: [], parked: [] })

    await fireEvent.click(screen.getAllByRole('button')[0])
    expect(await screen.findByRole('dialog', { name: 'bridge#412' })).toBeTruthy()

    await fireEvent.keyDown(window, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('opens the issue detail from a waiting line too', async () => {
    await renderInbox({
      cards: [],
      waiting: [card({ number: 412, kind: 'wait', reason: 'running' })],
      parked: [],
    })

    await fireEvent.click(screen.getByRole('button', { name: /bridge#412/ }))
    expect(await screen.findByRole('dialog', { name: 'bridge#412' })).toBeTruthy()
  })
})
