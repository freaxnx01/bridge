import { writable } from 'svelte/store'

// tokenPrompt holds the open token request ({ resolve, rejected }) or null.
export const tokenPrompt = writable(null)

// requestToken opens the token dialog and resolves with the saved token,
// or null when the user cancels.
export function requestToken({ rejected = false } = {}) {
  return new Promise((resolve) => {
    tokenPrompt.set({ resolve, rejected })
  })
}
