import { describe, it, expect } from 'vitest'
import { splitRunning, STUCK_ENRICHING_SECONDS } from './running.js'

const now = Date.parse('2026-10-27T20:00:00Z')
const ago = (s) => new Date(now - s * 1000).toISOString()

describe('splitRunning', () => {
  it('splitRunning_IdleSession_GoesToIdle', () => {
    const idle = { kind: 'session', status: 'idle', since: ago(60) }
    const busy = { kind: 'session', status: 'busy', since: ago(60) }
    const r = splitRunning([idle, busy], now)
    expect(r.active).toEqual([busy])
    expect(r.idle).toEqual([idle])
    expect(r.stuck).toEqual([])
  })

  it('splitRunning_EnrichingLabelUntouchedTooLong_GoesToStuck', () => {
    const old = { kind: 'enrichment', status: 'enriching', since: ago(STUCK_ENRICHING_SECONDS + 1) }
    const fresh = { kind: 'enrichment', status: 'enriching', since: ago(STUCK_ENRICHING_SECONDS - 60) }
    const r = splitRunning([old, fresh], now)
    expect(r.active).toEqual([fresh])
    expect(r.stuck).toEqual([old])
  })

  it('splitRunning_PipelineRun_AlwaysActive', () => {
    const run = { kind: 'pipeline', status: 'in_progress', since: ago(10 * 86400) }
    expect(splitRunning([run], now).active).toEqual([run])
  })

  it('splitRunning_KeepsInputOrder', () => {
    const a = { kind: 'pipeline', status: 'queued', since: ago(30) }
    const b = { kind: 'session', status: 'busy', since: ago(20) }
    expect(splitRunning([a, b], now).active).toEqual([a, b])
  })

  it('splitRunning_NoCards_EmptyGroups', () => {
    expect(splitRunning(undefined, now)).toEqual({ active: [], idle: [], stuck: [] })
  })
})
