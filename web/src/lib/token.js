const KEY = 'bridge.apiToken'

export function getToken() {
  try {
    return localStorage.getItem(KEY) || null
  } catch {
    return null
  }
}

export function setToken(token) {
  try {
    localStorage.setItem(KEY, token)
  } catch {
    // storage unavailable: the token lasts until the next prompt
  }
}

export function clearToken() {
  try {
    localStorage.removeItem(KEY)
  } catch {
    // nothing stored, nothing to clear
  }
}
