import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import ConfirmDialog from './ConfirmDialog.svelte'

describe('ConfirmDialog', () => {
  it('names repo, issue and the action', () => {
    render(ConfirmDialog, { repo: 'freaxnx01/bridge', number: 42, action: 'dispatch', onconfirm: vi.fn(), oncancel: vi.fn() })
    const dlg = screen.getByRole('dialog')
    expect(dlg.getAttribute('aria-modal')).toBe('true')
    expect(dlg.textContent).toContain('freaxnx01/bridge')
    expect(dlg.textContent).toContain('#42')
    expect(dlg.textContent).toContain('starts a paid Claude session')
  })

  it('calls oncancel on Escape and onconfirm on the confirm button', async () => {
    const onconfirm = vi.fn()
    const oncancel = vi.fn()
    render(ConfirmDialog, { repo: 'a/b', number: 1, action: 'enrich', onconfirm, oncancel })
    await fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    expect(oncancel).toHaveBeenCalled()
    await fireEvent.click(screen.getByRole('button', { name: /enrich/i }))
    expect(onconfirm).toHaveBeenCalled()
  })
})

describe('ConfirmDialog lock clearing', () => {
  it('describes clear-lock as label-only, with no paid session', () => {
    render(ConfirmDialog, { repo: 'a/b', number: 612, action: 'clear-lock', onconfirm: vi.fn(), oncancel: vi.fn() })
    const dlg = screen.getByRole('dialog')
    expect(dlg.textContent).toContain('Clear lock a/b #612?')
    expect(dlg.textContent).toContain('enrichment-ongoing')
    expect(dlg.textContent).not.toContain('paid Claude session')
  })

  it('describes clear-reenrich as clearing and then starting a paid session', () => {
    render(ConfirmDialog, { repo: 'a/b', number: 612, action: 'clear-reenrich', onconfirm: vi.fn(), oncancel: vi.fn() })
    const dlg = screen.getByRole('dialog')
    expect(dlg.textContent).toContain('Clear & re-enrich a/b #612?')
    expect(dlg.textContent).toContain('enrich-612')
    expect(dlg.textContent).toContain('starts a paid Claude session')
  })
})

describe('ConfirmDialog merge', () => {
  const detail = {
    pr: 9,
    baseRef: 'main',
    headSha: '96fae5fd5d9aa4022a3b5a57a69b50510a563891',
    checks: { state: 'green', passed: 6, total: 6 },
    review: { verdict: 'approve', current: true },
  }

  it('names the PR, the base branch and the evidence', () => {
    render(ConfirmDialog, { repo: 'freaxnx01/bridge', number: 5, action: 'merge', detail, onconfirm: vi.fn(), oncancel: vi.fn() })
    const dlg = screen.getByRole('dialog')
    expect(dlg.textContent).toContain('Merge PR #9 into main?')
    expect(dlg.textContent).toContain('squash')
    expect(dlg.textContent).toContain('checks 6/6')
    expect(dlg.textContent).toContain('review approve on 96fae5f (head)')
    expect(dlg.textContent).toContain('Closes #5')
    expect(dlg.textContent).toContain('The branch is deleted after merge')
    expect(dlg.textContent).not.toContain('paid Claude session')
  })

  it('confirms with the squash verb', async () => {
    const onconfirm = vi.fn()
    render(ConfirmDialog, { repo: 'a/b', number: 5, action: 'merge', detail, onconfirm, oncancel: vi.fn() })
    await fireEvent.click(screen.getByRole('button', { name: /Squash & merge/ }))
    expect(onconfirm).toHaveBeenCalled()
  })
})
