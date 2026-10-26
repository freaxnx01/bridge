import { get, writable } from 'svelte/store'

// tokenPrompt holds the open token request ({ resolve, rejected }) or null.
export const tokenPrompt = writable(null)

// requestToken opens the token dialog and resolves with the saved token,
// or null when the user cancels.
export function requestToken({ rejected = false } = {}) {
  return new Promise((resolve) => {
    // a request still waiting is abandoned: release its caller
    get(tokenPrompt)?.resolve(null)
    tokenPrompt.set({ resolve, rejected })
  })
}
