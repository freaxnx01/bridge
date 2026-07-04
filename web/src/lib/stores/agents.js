import { writable } from 'svelte/store'
import { get as apiGet } from '../api.js'
import { sseEvent } from './sse.js'

export const agents = writable([])

export async function loadAgents() {
  const data = await apiGet('/api/agents')
  agents.set(data ?? [])
}

sseEvent.subscribe(ev => {
  if (ev?.type === 'agents-updated') loadAgents()
})
