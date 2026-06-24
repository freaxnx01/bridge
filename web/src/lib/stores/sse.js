import { writable } from 'svelte/store'

// createSseStore wraps EventSource, auto-reconnects on error,
// and exposes the latest parsed event as a Svelte store.
export function createSseStore(url) {
  const { subscribe, set } = writable(null)
  let es = null

  function connect() {
    es = new EventSource(url)
    es.onmessage = (e) => {
      try { set(JSON.parse(e.data)) } catch {}
    }
    es.onerror = () => {
      es?.close()
      setTimeout(connect, 3000)
    }
  }

  if (typeof window !== 'undefined') connect()

  return { subscribe, disconnect: () => es?.close() }
}

export const sseEvent = createSseStore('/api/events')
