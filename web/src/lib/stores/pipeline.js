import { writable, get } from 'svelte/store'
import { get as apiGet } from '../api.js'
import { sseEvent } from './sse.js'

export const pipeline = writable(null)
export const pipelineError = writable(null)

export async function loadPipeline() {
  try {
    pipeline.set(await apiGet('/api/pipeline'))
    pipelineError.set(null)
  } catch (err) {
    pipelineError.set(err.message)
  }
}

sseEvent.subscribe((ev) => {
  if (ev?.type === 'pipeline-updated') loadPipeline()
})

const MIN_POLL_SECONDS = 5

// startPipelinePolling re-fetches the board every `intervalsSeconds.sessions`
// seconds (at least 5), because the server only pushes SSE when content
// changes: on a quiet board collectedAt would otherwise age on the page and
// healthy sources would show as stale. Returns the stop function.
export function startPipelinePolling() {
  let timer = null
  let stopped = false
  const delayMs = () => Math.max(MIN_POLL_SECONDS, get(pipeline)?.intervalsSeconds?.sessions ?? 0) * 1000
  const tick = async () => {
    await loadPipeline()
    if (!stopped) timer = setTimeout(tick, delayMs())
  }
  timer = setTimeout(tick, delayMs())
  return () => {
    stopped = true
    clearTimeout(timer)
  }
}
