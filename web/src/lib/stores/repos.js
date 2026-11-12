import { writable } from 'svelte/store'
import { get as apiGet } from '../api.js'
import { sseEvent } from './sse.js'

// REPOS_MAX_AGE_MS is how long a loaded repo list is taken as current. Unlike
// the board, /api/repos walks the whole repos tree on disk, while the list
// itself only changes when a repo is cloned — so a board update refreshes it
// at most this often.
export const REPOS_MAX_AGE_MS = 5 * 60 * 1000

// repos is every discovered repo, reduced to what the filter bar needs.
export const repos = writable([])

let lastLoaded = 0

export async function loadRepos() {
  try {
    const discovered = await apiGet('/api/repos')
    repos.set((discovered ?? []).map(({ forge, owner, name }) => ({ forge, owner, name })))
    lastLoaded = Date.now()
  } catch {
    // The repo list only feeds the filter bar: keep the last known one rather
    // than emptying the chips because one refresh failed. A failed load leaves
    // the list stale, so the next event tries again.
  }
}

sseEvent.subscribe((ev) => {
  if (ev?.type !== 'pipeline-updated') return
  if (Date.now() - lastLoaded > REPOS_MAX_AGE_MS) loadRepos()
})
