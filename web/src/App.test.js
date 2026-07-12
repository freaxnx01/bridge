import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/svelte'

vi.mock('./lib/stores/repos.js', () => ({
  loadRepos: vi.fn(),
  repos: { subscribe: (fn) => { fn([]); return () => {} } },
}))
vi.mock('./lib/stores/agents.js', () => ({
  loadAgents: vi.fn(),
  agents: { subscribe: (fn) => { fn([]); return () => {} } },
}))

describe('App', () => {
  beforeEach(() => vi.clearAllMocks())

  it('renders an Architecture section with the inlined SVG diagram', async () => {
    const { default: App } = await import('./App.svelte')
    render(App)

    const heading = screen.getByRole('heading', { name: 'Architecture' })
    expect(heading).toBeTruthy()

    const section = heading.closest('section')
    expect(section.querySelector('svg')).not.toBeNull()
  })
})
