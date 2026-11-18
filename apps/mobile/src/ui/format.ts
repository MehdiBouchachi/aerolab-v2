// Display helpers, kept identical in meaning to apps/web/src/app/format.ts so both apps word things the same way.
// Pure functions: tested in Node (format.test.ts).
import type { Source } from '../core/types'

export type Quality = 'valid' | 'invalid' | 'missing' | string
export interface ReadingValue { value: number | boolean | null; quality: Quality; unit?: string }
export interface Unit {
  id: string; name: string; connection: string; control_state: string | null; pump_commanded: string | null
  applied_program_version: number | null; is_simulated: boolean; last_state_at?: string | null
  essential_interlocks_ok?: boolean | null; control_state_note?: string | null; firmware_version?: string | null
  latest_readings: { effective_at_utc: string; outdated: boolean; time_quality?: string; readings: Record<string, ReadingValue> } | null
}
export interface Summary {
  units: { total: number; healthy: number; attention: number; stale?: number }
  active_incidents: { total?: number; critical: number; warning: number }
  site_stale?: boolean; contains_simulated_data?: boolean; observed_at?: string
}
export interface Incident {
  id: string; unit_id: string; severity: 'info' | 'warning' | 'critical' | string; code?: string; status: string
  title: string; cause?: string; suggested_action: string
  first_seen_at?: string; last_seen_at?: string; acknowledged_at?: string | null; resolved_at?: string | null; note?: string
}
export interface HistoryPoint { t: string; readings: Record<string, ReadingValue> }
export interface History { points: HistoryPoint[]; gaps: { from: string; to: string }[]; resolution: string; truncated: boolean; contains_simulated_data: boolean; from: string; to: string }
export interface Program { on_duration_seconds: number; off_duration_seconds: number; max_continuous_on_seconds: number }
export interface CropProfile {
  id: string; name: string; crop: string; variety?: string; version: number; unit_count: number
  thresholds: Record<string, { min?: number; max?: number }>; program?: Program; value_source: string; notes?: string
}
export function incidentTitle(title: string): string {
  return title.replace(/^([a-z][a-z0-9_]*) is (above|below) this unit's crop-profile range$/, (_, sensor: string, direction: string) => {
    const name = label(sensor)
    return `${name.charAt(0).toUpperCase()}${name.slice(1)} ${direction} crop range`
  })
}
export interface Applied { applied: { version: number; program: Program; applied_at: string } | null; controller_reported_version: number | null }

export function ago(iso: string | number | null | undefined, now = Date.now()): string {
  if (iso === null || iso === undefined) return 'never'
  const t = typeof iso === 'number' ? iso : Date.parse(iso)
  const s = Math.max(0, Math.round((now - t) / 1000))
  if (s < 5) return 'just now'
  if (s < 90) return `${s} s ago`
  if (s < 5400) return `${Math.round(s / 60)} min ago`
  if (s < 172800) return `${Math.round(s / 3600)} h ago`
  return `${Math.round(s / 86400)} days ago`
}

/** A missing/invalid value is words, never 0. */
export function showReading(r: ReadingValue | undefined): string {
  if (!r) return 'not reported'
  if (r.quality === 'missing') return 'no sensor'
  if (r.quality === 'invalid') return 'invalid'
  if (r.value === null) return 'no value'
  if (typeof r.value === 'boolean') return r.value ? 'OK' : 'LOW'
  return `${r.value}${r.unit ? ' ' + r.unit : ''}`
}

export const SENSOR_LABEL: Record<string, string> = {
  air_temp_c: 'Air temperature', relative_humidity_pct: 'Humidity', water_temp_c: 'Water temperature',
  ph: 'pH', ec_ms_cm: 'EC', reservoir_level_ok: 'Reservoir level', light_lux: 'Light',
}
export const SHORT_LABEL: Record<string, string> = { water_temp_c: 'Water', ph: 'pH', ec_ms_cm: 'EC', reservoir_level_ok: 'Reservoir' }
export const label = (k: string) => SENSOR_LABEL[k] ?? k.replace(/_/g, ' ')
export const CARD_KEYS = ['water_temp_c', 'ph', 'ec_ms_cm', 'reservoir_level_ok']
export const READING_ORDER = ['water_temp_c', 'ph', 'ec_ms_cm', 'reservoir_level_ok', 'air_temp_c', 'relative_humidity_pct', 'light_lux']
export const orderReadings = <T,>(r: Record<string, T>) => Object.entries(r).sort(([a], [b]) => rank(a) - rank(b))
const rank = (k: string) => { const i = READING_ORDER.indexOf(k); return i < 0 ? 99 : i }

export type Tone = 'ok' | 'warn' | 'crit' | 'muted'
/** Plain-language status; never colour-only (the badge always carries text). */
export function unitStatus(u: Pick<Unit, 'connection' | 'control_state'>): { text: string; tone: Tone } {
  if (u.connection === 'NEVER_SEEN') return { text: 'Not seen yet', tone: 'muted' }
  if (u.connection !== 'ONLINE') return { text: 'Not reporting', tone: 'warn' }
  switch (u.control_state) {
    case 'RUNNING': return { text: 'Running', tone: 'ok' }
    case 'DEGRADED': return { text: 'Degraded – sensor fault', tone: 'warn' }
    case 'PROTECTED_STOP': return { text: 'Protected stop', tone: 'crit' }
    case 'NEEDS_SETUP': return { text: 'Needs a programme', tone: 'warn' }
    case 'BOOT_SAFE': case 'SELF_CHECK': return { text: 'Starting (self-check)', tone: 'muted' }
    default: return { text: u.control_state ?? 'Unknown', tone: 'muted' }
  }
}

export const stripSimSuffix = (name: string) => name.replace(/\s*\(SIMULATED\)$/i, '')

export const timeQualityText = (q?: string) =>
  q === 'DEVICE_CLOCK' ? 'Controller clock' : q === 'ESTIMATED_FROM_UPTIME' ? 'Time estimated from controller uptime' : 'Time recorded when the Pi received it'

/**
 * API paths differ by source: the Pi (edge) serves unit resources at /api/v1/units/{id}, the cloud at
 * /api/v1/sites/{site}/units/{id}. Same mapping as apps/web/src/api/paths.ts.
 */
export function paths(source: Source, site: string) {
  const cloud = source === 'cloud'
  const edge = (path: string) => cloud ? `/api/v1/sites/${site}/edge/${path}` : `/api/v1/${path}`
  const unitBase = (id: string) => cloud ? `/api/v1/sites/${site}/units/${id}` : `/api/v1/units/${id}`
  return {
    summary: `/api/v1/sites/${site}/summary`,
    units: `/api/v1/sites/${site}/units?limit=100`,
    addUnit: edge(`sites/${site}/units`),
    crops: edge(`sites/${site}/crop-profiles`),
    session: cloud ? `/api/v1/session?site=${encodeURIComponent(site)}` : '/api/v1/session',
    storage: edge(`sites/${site}/storage`),
    controllers: edge(`sites/${site}/devices`),
    activity: edge(`sites/${site}/audit?limit=20`),
    users: cloud ? `/api/v1/sites/${site}/members` : '/api/v1/local/users',
    cloudLink: '/api/v1/cloud-link',
    incidents: (state: 'active' | 'resolved', limit = 100, unit?: string) => `/api/v1/sites/${site}/incidents?state=${state}&limit=${limit}${unit ? `&unit_id=${encodeURIComponent(unit)}` : ''}`,
    unit: unitBase,
    history: (id: string, from: string, to: string) => `${unitBase(id)}/readings/history?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    applied: (id: string) => `${unitBase(id)}/programs/applied`,
    acknowledge: (incident: string) => edge(`incidents/${incident}/acknowledge`),
  }
}

export interface Series { t: number; v: number }
/** Split into line segments; a gap between consecutive points breaks the line (never drawn across). */
export function segments(pts: Series[], gaps: { from: number; to: number }[]): Series[][] {
  const out: Series[][] = []; let cur: Series[] = []
  for (let i = 0; i < pts.length; i++) {
    if (i > 0 && gaps.some(g => pts[i - 1].t >= g.from - 1 && pts[i].t <= g.to + 1)) { if (cur.length) out.push(cur); cur = [] }
    cur.push(pts[i])
  }
  if (cur.length) out.push(cur)
  return out
}
/** Valid numeric points only; invalid/missing are returned separately and marked, never plotted as numbers. */
export function seriesOf(points: HistoryPoint[], key: string): { pts: Series[]; invalid: number[] } {
  const pts: Series[] = []; const invalid: number[] = []
  for (const p of points) {
    const r = p.readings[key]; const t = Date.parse(p.t)
    if (r && r.quality === 'valid' && typeof r.value === 'number') pts.push({ t, v: r.value })
    else invalid.push(t)
  }
  return { pts, invalid }
}
/** Scale with a minimum visible span so small noise is not exaggerated (same rule as the web). */
export function scaleOf(values: number[]): { lo: number; hi: number } {
  let lo = values.length ? Math.min(...values) : 0
  let hi = values.length ? Math.max(...values) : 1
  const mid = (lo + hi) / 2
  const minSpan = Math.max(0.5, Math.abs(mid) * 0.05)
  if (hi - lo < minSpan) { lo = mid - minSpan / 2; hi = mid + minSpan / 2 }
  const pad = (hi - lo) * 0.1
  return { lo: lo - pad, hi: hi + pad }
}
