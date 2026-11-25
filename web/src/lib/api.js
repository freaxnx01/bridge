export async function get(path) {
  const res = await fetch(path)
  if (!res.ok) throw new Error(`${path}: ${res.status} ${res.statusText}`)
  return res.json()
}

// getAuthed reads a token-guarded endpoint. Unlike get it throws the same
// {status, message} failure post throws, so the caller can tell a rejected
// token from a stale card.
export async function getAuthed(path, token) {
  const headers = {}
  if (token) headers.Authorization = `Bearer ${token}`
  const res = await fetch(path, { headers })
  if (!res.ok) throw await failure(res)
  return res.json()
}

export async function post(path, body, token) {
  const headers = { 'Content-Type': 'application/json' }
  if (token) headers.Authorization = `Bearer ${token}`
  const res = await fetch(path, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
  })
  if (!res.ok) throw await failure(res)
  return res.json().catch(() => ({}))
}

// failure turns a non-OK response into an Error carrying the server's own
// message and its status.
async function failure(res) {
  const body = await res.json().catch(() => ({ error: res.statusText }))
  const err = new Error(body.error || res.statusText)
  err.status = res.status
  return err
}
