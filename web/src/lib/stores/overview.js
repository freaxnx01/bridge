import { writable } from 'svelte/store'
import { get as apiGet } from '../api.js'
import { sseEvent } from './sse.js'

export const overview = writable(null)

export async function loadOverview() {
  const data = await apiGet('/api/overview')
  overview.set(data)
}

sseEvent.subscribe(ev => {
  if (ev?.type === 'overview-updated') loadOverview()
})
