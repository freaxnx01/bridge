import { writable } from 'svelte/store'
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
