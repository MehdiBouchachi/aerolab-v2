import type { CommandStatus, Quality, Unit } from '../api/types'

export function ago(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return 'never'
  const s = Math.max(0, Math.round((now - Date.parse(iso)) / 1000))
  if (s < 5) return 'just now'
  if (s < 90) return `${s} s ago`
  if (s < 5400) return `${Math.round(s / 60)} min ago`
  if (s < 172800) return `${Math.round(s / 3600)} h ago`
  return `${Math.round(s / 86400)} days ago`
}
export const clock = (iso: string) => new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'medium' })

/** Reading value for humans. A missing/invalid value is words, never 0. */
export function showReading(r: { value: number | boolean | null; quality: Quality; unit?: string } | undefined): string {
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
export const label = (k: string) => SENSOR_LABEL[k] ?? k.replace(/_/g, ' ')

export type Tone = 'ok' | 'warn' | 'crit' | 'muted'
/** Plain-language status; never colour-only (always paired with text). */
export function unitStatus(u: Unit): { text: string; tone: Tone } {
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
export const COMMAND_TEXT: Record<CommandStatus, { text: string; tone: Tone }> = {
  PENDING_SITE: { text: 'Pending – stored in the cloud, the site has not picked it up yet', tone: 'warn' },
  PENDING_DEVICE: { text: 'Pending – the site accepted it, waiting for the controller', tone: 'warn' },
  PENDING: { text: 'Pending – not sent yet', tone: 'warn' },
  SENT: { text: 'Pending – waiting for the controller', tone: 'warn' },
  APPLIED: { text: 'Applied – confirmed by the controller', tone: 'ok' },
  REJECTED: { text: 'Rejected by the controller', tone: 'crit' },
  CONFLICT: { text: 'Conflict – the controller has a different version', tone: 'crit' },
  EXPIRED: { text: 'Expired – it never reached the controller and will not run', tone: 'muted' },
  UNKNOWN: { text: 'Unknown – no confirmation; may or may not be applied', tone: 'warn' },
}
export const bytes = (n: number) => n > 1 << 30 ? `${(n / (1 << 30)).toFixed(1)} GiB` : `${(n / (1 << 20)).toFixed(1)} MiB`
