import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'

import DiffView from './DiffView.svelte'

const diff = (over = {}) => ({
  pr: 9,
  headSha: '96fae5fd5d9aa4022a3b5a57a69b50510a563891',
  truncated: false,
  files: [
    {
      path: 'a.go',
      status: 'modified',
      additions: 1,
      deletions: 0,
      patch: '@@ -1,2 +1,3 @@\n context\n+added\n-removed',
      patchMissing: false,
    },
  ],
  ...over,
})

const show = (over = {}) => render(DiffView, { diff: diff(over) })

describe('DiffView', () => {
  it('renders collapsed rows and expands a patch', async () => {
    show()
    const row = screen.getByRole('button', { name: /a\.go/ })
    expect(row.textContent).toMatch(/\+1/)
    expect(row.textContent).toMatch(/−0/)
    expect(document.querySelector('pre')).toBeNull()

    await fireEvent.click(row)
    const pre = document.querySelector('pre')
    expect(pre).toBeTruthy()
    expect(pre.querySelector('.add').textContent).toBe('+added')
    expect(pre.querySelector('.del').textContent).toBe('-removed')
    expect(pre.querySelector('.hunk').textContent).toBe('@@ -1,2 +1,3 @@')

    await fireEvent.click(row)
    expect(document.querySelector('pre')).toBeNull()
  })

  it('shows no-patch rows', async () => {
    show({
      files: [
        { path: 'img.png', status: 'added', additions: 0, deletions: 0, patch: '', patchMissing: true },
      ],
    })
    await fireEvent.click(screen.getByRole('button', { name: /img\.png/ }))
    expect(screen.getByText('no patch (binary or too large)')).toBeTruthy()
    expect(document.querySelector('pre')).toBeNull()
  })

  it('tags a status that is not a plain modification', () => {
    show({
      files: [
        { path: 'gone.go', status: 'removed', additions: 0, deletions: 9, patch: '@@\n-x', patchMissing: false },
      ],
    })
    expect(screen.getByText('removed')).toBeTruthy()
  })

  it('says when the file list is truncated', () => {
    show({ truncated: true })
    expect(screen.getByText('first 100 files shown')).toBeTruthy()
  })

  it('says so when the PR changes no files at all', () => {
    show({ files: [] })
    expect(screen.getByText('no files in this PR')).toBeTruthy()
  })
})
