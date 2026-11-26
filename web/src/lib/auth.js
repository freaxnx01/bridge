import { getAuthed, post } from './api.js'
import { clearToken, getToken } from './token.js'
import { requestToken } from './stores/tokenPrompt.js'

// ensureToken returns the stored token, asking for one when absent.
// Resolves null when the user declines.
export async function ensureToken() {
  return getToken() ?? (await requestToken())
}

// authedPost posts with the bearer token. A 401 clears the stored token and
// reopens the token dialog flagged as rejected before rethrowing.
export async function authedPost(path, body, token) {
  return reprompting(() => post(path, body, token))
}

// authedGet reads a token-guarded endpoint (today: the merge card's diff) with
// the same 401 handling as authedPost.
export async function authedGet(path, token) {
  return reprompting(() => getAuthed(path, token))
}

// reprompting runs one authenticated call, turning a rejected token into a
// fresh prompt. The error is always rethrown: only the caller knows what to
// say about a request that did not happen.
async function reprompting(call) {
  try {
    return await call()
  } catch (err) {
    if (err.status === 401) {
      clearToken()
      requestToken({ rejected: true })
    }
    throw err
  }
}
