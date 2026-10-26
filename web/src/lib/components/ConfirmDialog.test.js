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
