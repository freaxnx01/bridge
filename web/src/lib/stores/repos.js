import { writable } from 'svelte/store'
import { get as apiGet } from '../api.js'
import { sseEvent } from './sse.js'

// repos is every discovered repo, reduced to what the filter bar needs.
export const repos = writable([])

export async function loadRepos() {
  try {
    const discovered = await apiGet('/api/repos')
    repos.set((discovered ?? []).map(({ forge, owner, name }) => ({ forge, owner, name })))
  } catch {
    // The repo list only feeds the filter bar: keep the last known one rather
    // than emptying the chips because one refresh failed.
  }
}

sseEvent.subscribe((ev) => {
  if (ev?.type === 'pipeline-updated') loadRepos()
})
