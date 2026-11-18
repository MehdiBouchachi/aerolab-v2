import { describe, expect, it } from 'vitest'
import { ago, paths, scaleOf, segments, seriesOf, showReading, unitStatus } from './format'

describe('paths follow the source', () => {
  it('routes site-owned tools through the cloud edge relay', () => {
    expect(paths('local', 'S1').crops).toBe('/api/v1/sites/S1/crop-profiles')
    expect(paths('local', 'S1').addUnit).toBe('/api/v1/sites/S1/units')
    const cloud = paths('cloud', 'S1')
    expect(cloud.crops).toBe('/api/v1/sites/S1/edge/sites/S1/crop-profiles')
    expect(cloud.addUnit).toBe('/api/v1/sites/S1/edge/sites/S1/units')
    expect(cloud.acknowledge('a1')).toBe('/api/v1/sites/S1/edge/incidents/a1/acknowledge')
    expect(cloud.incidents('active', 100, 'unit/1')).toContain('unit_id=unit%2F1')
  })
  it('local (edge) unit resources are not site-scoped', () => {
    const p = paths('local', 'S1')
    expect(p.unit('u1')).toBe('/api/v1/units/u1')
    expect(p.applied('u1')).toBe('/api/v1/units/u1/programs/applied')
    expect(p.history('u1', 'a', 'b')).toBe('/api/v1/units/u1/readings/history?from=a&to=b')
    expect(p.summary).toBe('/api/v1/sites/S1/summary')
  })
  it('cloud unit resources are site-scoped', () => {
    const p = paths('cloud', 'S1')
    expect(p.unit('u1')).toBe('/api/v1/sites/S1/units/u1')
    expect(p.applied('u1')).toBe('/api/v1/sites/S1/units/u1/programs/applied')
  })
})

describe('display wording', () => {
  it('never shows a missing or invalid reading as a number', () => {
    expect(showReading({ value: 0, quality: 'missing' })).toBe('no sensor')
    expect(showReading({ value: 7, quality: 'invalid' })).toBe('invalid')
    expect(showReading({ value: null, quality: 'valid' })).toBe('no value')
    expect(showReading({ value: false, quality: 'valid' })).toBe('LOW')
    expect(showReading({ value: 6.1, quality: 'valid', unit: 'mS/cm' })).toBe('6.1 mS/cm')
  })
  it('status is plain language and an offline unit is never "Running"', () => {
    expect(unitStatus({ connection: 'STALE', control_state: 'RUNNING' })).toEqual({ text: 'Not reporting', tone: 'warn' })
    expect(unitStatus({ connection: 'ONLINE', control_state: 'NEEDS_SETUP' }).text).toBe('Needs a programme')
  })
  it('ages read like the web', () => {
    expect(ago(1000, 3000)).toBe('just now')
    expect(ago(0, 60_000)).toBe('60 s ago')
    expect(ago(null)).toBe('never')
  })
})

describe('history geometry', () => {
  it('breaks lines at gaps and keeps invalid points out of the series', () => {
    const pts = [{ t: 0, v: 1 }, { t: 10, v: 2 }, { t: 100, v: 3 }]
    expect(segments(pts, [{ from: 10, to: 100 }])).toHaveLength(2) // a gap spans from the last point before it to the first after
    expect(segments(pts, [])).toHaveLength(1)
    const s = seriesOf([{ t: '1970-01-01T00:00:00Z', readings: { ph: { value: 6, quality: 'valid' } } }, { t: '1970-01-01T00:00:01Z', readings: { ph: { value: 99, quality: 'invalid' } } }], 'ph')
    expect(s.pts).toEqual([{ t: 0, v: 6 }])
    expect(s.invalid).toEqual([1000])
  })
  it('keeps a minimum span so flat noise is not exaggerated', () => {
    const { lo, hi } = scaleOf([6, 6.01])
    expect(hi - lo).toBeGreaterThanOrEqual(0.5)
  })
})
