import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import { writable, get } from 'svelte/store'

import { repoFilter, clearFilter } from '../lib/stores/repoFilter.js'

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
  loadIssueDetail: vi.fn(async (repo, number) => ({ issue: { repo, number, title: `title ${number}`, labels: [] }, runs: [] })),
}))

const card = (over = {}) => ({
  forge: 'github', repo: 'freaxnx01/bridge', number: 412, title: 'fix(nav): x', url: 'u',
  kind: 'human', reason: 'review blocked', since: '2026-11-11T10:00:00Z', ...over,
})

const cardA = (over = {}) => card({ repo: 'o/a', number: 1, title: 'card a', ...over })
const cardB = (over = {}) => card({ repo: 'p/b', number: 2, title: 'card b', ...over })

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
    clearFilter()
  })

  describe('repo filter', () => {
    it('keeps only the selected repos cards', async () => {
      repoFilter.set({ forge: '', owner: '', repos: ['github:o/a'] })
      await renderInbox({ cards: [cardA(), cardB()], waiting: [], parked: [] })

      expect(screen.getByText('card a')).toBeTruthy()
      expect(screen.queryByText('card b')).toBeNull()
    })

    it('applies a forge and owner filter to waiting lines and parked cards too', async () => {
      repoFilter.set({ forge: 'github', owner: 'p', repos: [] })
      await renderInbox({
        cards: [cardA(), cardB()],
        waiting: [cardA({ number: 3, kind: 'wait', reason: 'running' }), cardB({ number: 4, kind: 'wait', reason: 'running' })],
        parked: [cardA({ number: 5, kind: 'parked', reason: 'parked' }), cardB({ number: 6, kind: 'parked', reason: 'parked' })],
      })

      expect(screen.queryByText('card a')).toBeNull()
      expect(screen.getByText('card b')).toBeTruthy()
      expect(screen.queryByText('a#3')).toBeNull()
      expect(screen.getByText('b#4')).toBeTruthy()
      expect(screen.getByRole('button', { name: /Parked \(1\)/ })).toBeTruthy()
    })

    it('offers to clear the filter when it hides everything', async () => {
      repoFilter.set({ forge: '', owner: '', repos: ['github:gone/away'] })
      await renderInbox({ cards: [cardA()], waiting: [], parked: [] })

      expect(screen.getByText('Nothing for the selected repos.')).toBeTruthy()
      expect(screen.queryByText('Nothing needs you.')).toBeNull()

      await fireEvent.click(screen.getByRole('button', { name: '✕ clear filter' }))
      expect(get(repoFilter)).toEqual({ forge: '', owner: '', repos: [] })
    })

    it('holds back the filter empty state until the payload has loaded', async () => {
      repoFilter.set({ forge: '', owner: '', repos: ['github:o/a'] })
      await renderInbox(null)

      expect(screen.queryByText('Nothing for the selected repos.')).toBeNull()
    })

    it('shows the load error, not the filter empty state, when the payload failed', async () => {
      repoFilter.set({ forge: '', owner: '', repos: ['github:o/a'] })
      await renderInbox(null, '/api/inbox: 500 Internal Server Error')

      expect(screen.queryByText('Nothing for the selected repos.')).toBeNull()
      expect(screen.getByText(/500 Internal Server Error/)).toBeTruthy()
    })

    it('still warns about failed comment reads when the filter hides every card', async () => {
      repoFilter.set({ forge: '', owner: '', repos: ['github:gone/away'] })
      await renderInbox({ cards: [cardA()], waiting: [], parked: [], errors: { 'comments o/a#5': 'x' } })

      expect(screen.getByText('Nothing for the selected repos.')).toBeTruthy()
      expect(screen.getByText(/Could not read comments: o\/a#5 — x/)).toBeTruthy()
    })
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

  it('shows a warning line per failed comment fetch, with the issue and the reason', async () => {
    await renderInbox({ cards: [], waiting: [], parked: [], errors: { 'comments o/a#5': 'boom' } })
    expect(screen.getByText(/Could not read comments: o\/a#5 — boom/)).toBeTruthy()
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

  it('switches the open detail to the card opened next', async () => {
    await renderInbox({
      cards: [card(), card({ number: 500, title: 'fix(api): y' })],
      waiting: [],
      parked: [],
    })

    await fireEvent.click(screen.getByRole('button', { name: /bridge#412/ }))
    expect(await screen.findByText('title 412')).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: /bridge#500/ }))

    expect(await screen.findByRole('dialog', { name: 'bridge#500' })).toBeTruthy()
    expect(await screen.findByText('title 500')).toBeTruthy()
    expect(screen.queryByText('title 412')).toBeNull()
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
