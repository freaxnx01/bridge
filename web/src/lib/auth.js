import { post } from './api.js'
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
  try {
    return await post(path, body, token)
  } catch (err) {
    if (err.status === 401) {
      clearToken()
      requestToken({ rejected: true })
    }
    throw err
  }
}
