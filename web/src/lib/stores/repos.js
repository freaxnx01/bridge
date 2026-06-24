import { writable } from 'svelte/store'
import { get as apiGet } from '../api.js'
import { sseEvent } from './sse.js'

export const repos = writable([])

export async function loadRepos() {
  const data = await apiGet('/api/repos')
  repos.set(data)
}

sseEvent.subscribe(ev => {
  if (ev?.type === 'overview-updated' || ev?.type === 'repo-updated') loadRepos()
})
