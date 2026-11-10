import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import { get } from 'svelte/store'
import TokenDialog from './TokenDialog.svelte'
import { tokenPrompt, requestToken } from '../stores/tokenPrompt.js'

describe('TokenDialog', () => {
  beforeEach(() => { localStorage.clear(); tokenPrompt.set(null) })

  it('saves the token and resolves the pending request', async () => {
    render(TokenDialog)
    const p = requestToken()
    const input = await screen.findByLabelText('Token')
    await fireEvent.input(input, { target: { value: 'abc' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await p).toBe('abc')
    expect(localStorage.getItem('bridge.apiToken')).toBe('abc')
    expect(get(tokenPrompt)).toBeNull()
  })

  it('resolves null on cancel and says Token rejected when flagged', async () => {
    render(TokenDialog)
    const p = requestToken({ rejected: true })
    expect(await screen.findByText('Token rejected')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(await p).toBeNull()
  })
})
