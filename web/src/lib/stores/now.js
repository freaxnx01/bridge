import { readable } from 'svelte/store'

// now ticks once a second so ages stay correct without server pushes.
export const now = readable(Date.now(), (set) => {
  const timer = setInterval(() => set(Date.now()), 1000)
  return () => clearInterval(timer)
})
