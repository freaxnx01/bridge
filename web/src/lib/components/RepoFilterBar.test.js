import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/svelte'
import { get } from 'svelte/store'

import RepoFilterBar from './RepoFilterBar.svelte'
import { repoFilter, clearFilter } from '../stores/repoFilter.js'

const repos = [
  { forge: 'github', owner: 'freaxnx01', name: 'bridge' },
  { forge: 'github', owner: 'freaxnx01', name: 'agent-workflow' },
  { forge: 'github', owner: 'anim-bossinfo-ch', name: 'ArchiveRestApiNextGen' },
  { forge: 'ado', owner: 'personal', name: 'sandbox' },
]

const noFilter = { forge: '', owner: '', repos: [] }

function renderBar(filter = noFilter, counts = {}) {
  return render(RepoFilterBar, { repos, filter, counts })
}

const chipTexts = (name) =>
  within(screen.getByRole('group', { name })).getAllByRole('button').map((b) => b.textContent.replace(/\s+/g, ' ').trim())

async function openDropdown() {
  await fireEvent.click(screen.getByRole('button', { name: /▾$/ }))
}

describe('RepoFilterBar', () => {
  beforeEach(() => clearFilter())

  it('lists All plus one chip per forge present, with repo counts, in forge order', () => {
    renderBar()
    expect(chipTexts('Forge')).toEqual(['All', 'GitHub 3', 'ADO 1'])
    expect(screen.getByRole('button', { name: 'All' }).getAttribute('aria-pressed')).toBe('true')
    expect(screen.queryByRole('group', { name: 'Owner' })).toBeNull()
  })

  it('picks a forge from its chip', async () => {
    renderBar()
    await fireEvent.click(screen.getByRole('button', { name: 'ADO 1' }))
    expect(get(repoFilter).forge).toBe('ado')
  })

  it('shows the owners of the picked forge and picks one', async () => {
    renderBar({ ...noFilter, forge: 'github' })
    expect(chipTexts('Owner')).toEqual(['All', 'anim-bossinfo-ch 1', 'freaxnx01 2'])

    await fireEvent.click(screen.getByRole('button', { name: 'freaxnx01 2' }))
    expect(get(repoFilter).owner).toBe('freaxnx01')
  })

  it('labels the dropdown button by the selection', () => {
    const { unmount } = renderBar()
    expect(screen.getByRole('button', { name: /▾$/ }).textContent).toContain('All repos')
    unmount()

    const one = renderBar({ ...noFilter, repos: ['github:freaxnx01/bridge'] })
    expect(screen.getByRole('button', { name: /▾$/ }).textContent).toContain('bridge')
    one.unmount()

    renderBar({ ...noFilter, repos: ['github:freaxnx01/bridge', 'ado:personal/sandbox'] })
    expect(screen.getByRole('button', { name: /▾$/ }).textContent).toContain('2 repos')
  })

  it('groups the dropdown by forge and owner', async () => {
    renderBar()
    await openDropdown()

    expect(screen.getByText('GitHub')).toBeTruthy()
    expect(screen.getByText('ADO')).toBeTruthy()
    expect(screen.getByText('freaxnx01')).toBeTruthy()
    expect(screen.getByText('personal')).toBeTruthy()
  })

  it('shows only the picked forge, and only the picked owner', async () => {
    const { unmount } = renderBar({ ...noFilter, forge: 'ado' })
    await openDropdown()
    expect(screen.getByText('ADO')).toBeTruthy()
    expect(screen.queryByText('GitHub')).toBeNull()
    unmount()

    renderBar({ ...noFilter, forge: 'github', owner: 'freaxnx01' })
    await openDropdown()
    expect(screen.getByText('freaxnx01')).toBeTruthy()
    expect(screen.queryByText('anim-bossinfo-ch')).toBeNull()
  })

  it('narrows the rows by the search text', async () => {
    renderBar()
    await openDropdown()
    await fireEvent.input(screen.getByPlaceholderText('filter repos…'), { target: { value: 'brid' } })

    expect(screen.getByRole('checkbox', { name: 'freaxnx01/bridge' })).toBeTruthy()
    expect(screen.queryByRole('checkbox', { name: 'freaxnx01/agent-workflow' })).toBeNull()
  })

  it('selects a single repo from its row', async () => {
    renderBar()
    await openDropdown()
    await fireEvent.click(screen.getByRole('checkbox', { name: 'freaxnx01/bridge' }))

    expect(get(repoFilter).repos).toEqual(['github:freaxnx01/bridge'])
  })

  it('selects every repo of an owner from its all toggle, and clears with All repos', async () => {
    renderBar()
    await openDropdown()
    await fireEvent.click(screen.getByRole('checkbox', { name: 'all freaxnx01' }))

    expect(get(repoFilter).repos.sort()).toEqual(['github:freaxnx01/agent-workflow', 'github:freaxnx01/bridge'])

    await fireEvent.click(screen.getByRole('checkbox', { name: 'All repos' }))
    expect(get(repoFilter).repos).toEqual([])
  })

  it('shows a repo its item count on the current tab, and nothing at zero', async () => {
    renderBar(noFilter, { 'github:freaxnx01/bridge': 4, 'github:freaxnx01/agent-workflow': 0 })
    await openDropdown()

    const row = (name) => screen.getByRole('checkbox', { name }).closest('li')
    expect(row('freaxnx01/bridge').textContent).toContain('4')
    expect(row('freaxnx01/agent-workflow').textContent.replace(/\s/g, '')).toBe('agent-workflow')
  })

  it('closes the dropdown on Escape', async () => {
    renderBar()
    await openDropdown()
    expect(screen.getByPlaceholderText('filter repos…')).toBeTruthy()

    await fireEvent.keyDown(window, { key: 'Escape' })
    expect(screen.queryByPlaceholderText('filter repos…')).toBeNull()
  })

  it('offers clear only while something is filtered, and resets all three', async () => {
    const { unmount } = renderBar()
    expect(screen.queryByRole('button', { name: '✕ clear' })).toBeNull()
    unmount()

    renderBar({ forge: 'github', owner: 'freaxnx01', repos: ['github:freaxnx01/bridge'] })
    await fireEvent.click(screen.getByRole('button', { name: '✕ clear' }))

    expect(get(repoFilter)).toEqual(noFilter)
  })

  it('keeps a selected repo the list does not know', () => {
    repoFilter.set({ ...noFilter, repos: ['github:gone/away'] })
    renderBar({ ...noFilter, repos: ['github:gone/away'] })

    expect(get(repoFilter).repos).toEqual(['github:gone/away'])
  })
})
