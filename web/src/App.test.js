import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'

vi.mock('./lib/stores/pipeline.js', async () => {
  const { writable } = await import('svelte/store')
  return {
    pipeline: writable(null),
    pipelineError: writable(null),
    loadPipeline: vi.fn(),
    refreshPipeline: vi.fn(),
  }
})

describe('App', () => {
  beforeEach(() => vi.clearAllMocks())

  it('lands on the Pipeline view', async () => {
    const { default: App } = await import('./App.svelte')
    render(App)
    expect(screen.getByText(/RUNNING NOW/)).toBeTruthy()
    expect(screen.queryByRole('heading', { name: 'Architecture' })).toBeNull()
  })

  it('keeps the Architecture section reachable via the nav with the inlined SVG', async () => {
    const { default: App } = await import('./App.svelte')
    render(App)
    await fireEvent.click(screen.getByRole('button', { name: 'Arch' }))

    const heading = screen.getByRole('heading', { name: 'Architecture' })
    expect(heading.closest('section').querySelector('svg')).not.toBeNull()
  })
})
