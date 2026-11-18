import { useEffect, useState } from 'react'
import { api, ApiError } from '../api/client'
import type { Command, Program, Unit } from '../api/types'
import { useApp } from '../app/context'
import { IS_CLOUD, P } from '../api/paths'
import { COMMAND_TEXT } from '../app/format'
import { Badge } from '../components/ui'
import { Icon } from '../components/Icon'

export const BOUNDS = { on: [1, 3600], off: [1, 86400], max: [1, 3600] } as const
export interface Draft { on: string; off: string; max: string }

/** Client-side hints only; the Pi and the controller re-validate everything. */
export function validateDraft(d: Draft): Partial<Record<keyof Draft, string>> {
  const e: Partial<Record<keyof Draft, string>> = {}
  const num = (s: string) => (/^\d+$/.test(s) ? Number(s) : NaN)
  const on = num(d.on), off = num(d.off), max = num(d.max)
  if (!(on >= BOUNDS.on[0] && on <= BOUNDS.on[1])) e.on = `Whole seconds, ${BOUNDS.on[0]}–${BOUNDS.on[1]}.`
  if (!(off >= BOUNDS.off[0] && off <= BOUNDS.off[1])) e.off = `Whole seconds, ${BOUNDS.off[0]}–${BOUNDS.off[1]}.`
  if (!(max >= BOUNDS.max[0] && max <= BOUNDS.max[1])) e.max = `Whole seconds, ${BOUNDS.max[0]}–${BOUNDS.max[1]}.`
  else if (!e.on && max < on) e.max = 'Must be at least the pump-on time.'
  return e
}
export const toProgram = (d: Draft): Program => ({ mode: 'TIMED_CYCLE', on_duration_seconds: +d.on, off_duration_seconds: +d.off, max_continuous_on_seconds: +d.max, require_reservoir_ok: true })

const FINAL = ['APPLIED', 'REJECTED', 'CONFLICT', 'EXPIRED']
const PENDING = ['PENDING', 'SENT', 'PENDING_SITE', 'PENDING_DEVICE']

export function ProgramEditor({ unit, pending, onChanged }: { unit: Unit; pending: Command | null; onChanged: () => void }) {
  const { can, subscribe, session } = useApp()
  const site = session!.site!.id
  const [draft, setDraft] = useState<Draft>({ on: '', off: '', max: '' })
  const [touched, setTouched] = useState<Partial<Record<keyof Draft, boolean>>>({})
  const [review, setReview] = useState(false)
  const [cmd, setCmd] = useState<Command | null>(pending)
  const [err, setErr] = useState<ApiError | null>(null)
  const [busy, setBusy] = useState(false)
  const errs = validateDraft(draft)
  const applied = unit.applied_program_version ?? 0
  const inflight = cmd !== null && PENDING.includes(cmd.status)

  useEffect(() => { if (pending) setCmd(pending) }, [pending])
  // follow the command to its end: live events + a 1 s poll as safety net
  useEffect(() => {
    if (!cmd || FINAL.includes(cmd.status)) return
    const id = cmd.id
    const refresh = () => api<Command>('GET', P.command(site, id)).then(c => { setCmd(c); if (FINAL.includes(c.status)) onChanged() }).catch(() => {})
    const t = window.setInterval(refresh, 1000)
    const off = subscribe(e => { if (e.type === 'program.updated') refresh() })
    return () => { window.clearInterval(t); off() }
  }, [cmd?.id, cmd?.status, subscribe, onChanged]) // eslint-disable-line react-hooks/exhaustive-deps

  if (!can('edit_program')) return <p className="muted">Only an administrator can change the irrigation programme. Your role can view it.</p>

  const blocked = unit.connection !== 'ONLINE' ? 'The controller is not reporting, so a change cannot be delivered right now.' : null
  const send = async () => {
    setBusy(true); setErr(null)
    try {
      const body = { command_id: crypto.randomUUID(), expected_program_version: applied, program: toProgram(draft) }
      const c = await api<Command>('POST', P.programRequest(site, unit.id), IS_CLOUD ? { ...body, unit_id: unit.id } : body)
      setCmd(c); setReview(false)
    } catch (x) { setErr(x as ApiError); if ((x as ApiError).code === 'PROGRAM_VERSION_CONFLICT') onChanged() } finally { setBusy(false) }
  }
  const field = (key: keyof Draft, title: string, hint: string) => {
    const id = `programme-${key}`
    const invalid = Boolean(touched[key] && errs[key])
    return <div className="duration-field" key={key}>
      <label htmlFor={id}>{title}</label>
      <div className="duration-input"><input id={id} inputMode="numeric" value={draft[key]} onChange={e => { setDraft({ ...draft, [key]: e.target.value }); setReview(false) }} onBlur={() => setTouched(t => ({ ...t, [key]: true }))} aria-invalid={invalid} aria-describedby={`${id}-${invalid ? 'error' : 'hint'}`} /><span aria-hidden="true">sec</span></div>
      {invalid ? <span id={`${id}-error`} className="field-error">{errs[key]}</span> : <span id={`${id}-hint`} className="field-hint">{hint}</span>}
    </div>
  }

  return (
    <section className="programme-editor" aria-label="Programme editor">
      <header className="programme-editor-heading">
        <h3>{IS_CLOUD ? 'Request new timing' : 'Set irrigation timing'}</h3>
        <div className="programme-context"><span>Current version <b>{applied || 'none'}</b></span><span>Proposed version <b>{applied + 1}</b></span></div>
      </header>
      <p className="programme-guidance muted small">Use timing approved for your crop. Changes take effect after the controller confirms them.</p>
      {cmd && (
        <div className={`note ${COMMAND_TEXT[cmd.status].tone}`} role="status">
          <Badge tone={COMMAND_TEXT[cmd.status].tone}>{COMMAND_TEXT[cmd.status].text}</Badge>
          <div className="small">Version {cmd.new_program_version} · {cmd.attempts !== undefined ? `sent ${cmd.attempts}×` : ''} {cmd.reason_code ? `· ${cmd.reason_code}` : ''}</div>
          {inflight && <div className="small">The controller has not confirmed. The unit keeps running its previous programme until it does.{IS_CLOUD ? ' Remote requests expire in a few minutes if the site cannot pick them up, and then never run.' : ''}</div>}
          {cmd.status === 'UNKNOWN' && <div className="small">Do not press send again blindly – check the unit's applied version above first.</div>}
        </div>)}
      {err && <div className="note error" role="alert">{err.code === 'PROGRAM_VERSION_CONFLICT' ? `The controller already runs version ${String(err.details.actual_version)}. Review the current programme and try again.` : err.message}</div>}
      <fieldset disabled={inflight || busy}>
        <div className="programme-form-group"><h4>Timing and safety</h4><div className="duration-grid">
          {field('on', 'Pump on', '1–3,600 seconds')}
          {field('off', 'Pump off', '1–86,400 seconds')}
          {field('max', 'Max continuous on', '1–3,600 seconds, at least pump-on time')}
        </div></div>
        <div className="protection-note"><Icon name="check" size={17} /><strong>Reservoir protection is always on</strong></div>
        {!review
          ? <button className="primary" disabled={!!blocked} onClick={() => { setTouched({ on: true, off: true, max: true }); if (!Object.keys(errs).length) setReview(true) }}>Review change</button>
          : (
            <div className="review">
              <b>Confirm:</b> pump runs {draft.on} s every {Number(draft.on) + Number(draft.off)} s (off {draft.off} s), never longer than {draft.max} s at a time, on <b>{unit.name}</b> only.
              <div><button className="primary" onClick={() => void send()} disabled={busy}>{busy ? 'Sending…' : 'Send to controller'}</button> <button className="secondary" onClick={() => setReview(false)}>Edit</button></div>
            </div>)}
        {blocked && <p className="field-error">{blocked}</p>}
      </fieldset>
    </section>
  )
}
