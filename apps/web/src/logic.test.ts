import { describe, expect, it } from 'vitest'
import { parseSse } from './api/client'
import { segments, seriesOf } from './components/Chart'
import { validateDraft, toProgram } from './pages/ProgramEditor'
import { ago, showReading, unitStatus, COMMAND_TEXT } from './app/format'
import type { HistoryPoint, Unit } from './api/types'

describe('SSE parser', () => {
  it('returns complete events and keeps the partial remainder', () => {
    const p = parseSse('event: hello\ndata: {"a":1}\n\nevent: unit.state\ndata: {"b"')
    expect(p.events).toEqual([{ type: 'hello', data: { a: 1 } }])
    expect(p.rest).toBe('event: unit.state\ndata: {"b"')
  })
  it('ignores comments/pings and malformed data', () => {
    expect(parseSse(': ping\n\nevent: x\ndata: {nope}\n\n').events).toEqual([])
  })
})

describe('chart honesty', () => {
  const pt = (t: number, r: HistoryPoint['readings']): HistoryPoint => ({ t: new Date(t).toISOString(), snapshots: 1, time_quality: 'DEVICE_CLOCK', readings: r })
  it('never plots invalid or missing readings as numbers', () => {
    const { pts, invalid } = seriesOf([
      pt(1000, { ph: { value: 6, quality: 'valid' } }),
      pt(2000, { ph: { value: null, quality: 'invalid' } }),
      pt(3000, { ph: { value: null, quality: 'missing' } }),
      pt(4000, { ph: { value: 0, quality: 'invalid' } }),
    ], 'ph')
    expect(pts).toHaveLength(1)
    expect(invalid).toHaveLength(3)
  })
  it('breaks the line across a gap instead of interpolating', () => {
    const pts = [{ t: 0, v: 1 }, { t: 60000, v: 2 }, { t: 720000, v: 3 }, { t: 780000, v: 4 }]
    const segs = segments(pts, [{ from: 60000, to: 720000 }])
    expect(segs.map(s => s.length)).toEqual([2, 2])
  })
})

describe('programme draft validation', () => {
  it('accepts a bounded draft and builds a full programme with protection on', () => {
    const d = { on: '5', off: '60', max: '10' }
    expect(validateDraft(d)).toEqual({})
    expect(toProgram(d)).toEqual({ mode: 'TIMED_CYCLE', on_duration_seconds: 5, off_duration_seconds: 60, max_continuous_on_seconds: 10, require_reservoir_ok: true })
  })
  it('rejects empty, fractional, negative, oversized and inconsistent values', () => {
    expect(validateDraft({ on: '', off: '60', max: '10' }).on).toBeTruthy()
    expect(validateDraft({ on: '1.5', off: '60', max: '10' }).on).toBeTruthy()
    expect(validateDraft({ on: '-1', off: '60', max: '10' }).on).toBeTruthy()
    expect(validateDraft({ on: '5', off: '999999', max: '10' }).off).toBeTruthy()
    expect(validateDraft({ on: '20', off: '60', max: '10' }).max).toMatch(/at least/)
  })
})

describe('plain-language state', () => {
  it('shows missing/invalid readings as words, never zero', () => {
    expect(showReading({ value: null, quality: 'missing' })).toBe('no sensor')
    expect(showReading({ value: null, quality: 'invalid' })).toBe('invalid')
    expect(showReading({ value: 0, quality: 'invalid' })).toBe('invalid')
    expect(showReading({ value: 6.1, quality: 'valid' })).toBe('6.1')
    expect(showReading({ value: false, quality: 'valid' })).toBe('LOW')
    expect(showReading(undefined)).toBe('not reported')
  })
  const base = { connection: 'ONLINE', control_state: 'RUNNING' } as Unit
  it('a controller that is not reporting is never "Running"', () => {
    expect(unitStatus(base).text).toBe('Running')
    expect(unitStatus({ ...base, connection: 'STALE', control_state: 'UNKNOWN_STALE' }).text).toBe('Not reporting')
    expect(unitStatus({ ...base, control_state: 'PROTECTED_STOP' }).tone).toBe('crit')
  })
  it('pending is never worded as applied', () => {
    for (const s of ['PENDING', 'SENT'] as const) expect(COMMAND_TEXT[s].text).toMatch(/^Pending/)
    expect(COMMAND_TEXT.UNKNOWN.text).toMatch(/may or may not/)
    expect(COMMAND_TEXT.EXPIRED.text).toMatch(/will not run/)
  })
  it('relative times', () => {
    const now = Date.parse('2026-11-12T12:00:00Z')
    expect(ago('2026-11-12T11:59:30Z', now)).toBe('30 s ago')
    expect(ago(null)).toBe('never')
  })
})
