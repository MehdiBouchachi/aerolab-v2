import { useEffect, useState } from 'react'
import { api, ApiError } from '../api/client'
import type { Command, Program, Unit } from '../api/types'
import { useApp } from '../app/context'
import { COMMAND_TEXT } from '../app/format'
import { Badge } from '../components/ui'

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

export function ProgramEditor({ unit, pending, onChanged }: { unit: Unit; pending: Command | null; onChanged: () => void }) {
  const { can, subscribe } = useApp()
  const [draft, setDraft] = useState<Draft>({ on: '', off: '', max: '' })
  const [touched, setTouched] = useState(false)
  const [review, setReview] = useState(false)
  const [cmd, setCmd] = useState<Command | null>(pending)
  const [err, setErr] = useState<ApiError | null>(null)
  const [busy, setBusy] = useState(false)
  const errs = validateDraft(draft)
  const applied = unit.applied_program_version ?? 0
  const inflight = cmd !== null && (cmd.status === 'PENDING' || cmd.status === 'SENT')

  useEffect(() => { if (pending) setCmd(pending) }, [pending])
  // follow the command to its end: live events + a 1 s poll as safety net
  useEffect(() => {
    if (!cmd || FINAL.includes(cmd.status)) return
    const id = cmd.id
    const refresh = () => api<Command>('GET', `/api/v1/commands/${id}`).then(c => { setCmd(c); if (FINAL.includes(c.status)) onChanged() }).catch(() => {})
    const t = window.setInterval(refresh, 1000)
    const off = subscribe(e => { if (e.type === 'program.updated') refresh() })
    return () => { window.clearInterval(t); off() }
  }, [cmd?.id, cmd?.status, subscribe, onChanged]) // eslint-disable-line react-hooks/exhaustive-deps

  if (!can('edit_program')) return <p className="muted">Only an administrator can change the irrigation programme. Your role can view it.</p>

  const blocked = unit.connection !== 'ONLINE' ? 'The controller is not reporting, so a change cannot be delivered right now.' : null
  const send = async () => {
    setBusy(true); setErr(null)
    try {
      const c = await api<Command>('POST', `/api/v1/units/${unit.id}/program-requests`, { command_id: crypto.randomUUID(), expected_program_version: applied, program: toProgram(draft) })
      setCmd(c); setReview(false)
    } catch (x) { setErr(x as ApiError); if ((x as ApiError).code === 'PROGRAM_VERSION_CONFLICT') onChanged() } finally { setBusy(false) }
  }

  return (
    <section aria-label="Programme editor">
      <h3>Change programme</h3>
      <p className="muted small">Target: <b>{unit.name}</b> · currently applied version <b>{applied || 'none'}</b> → new version <b>{applied + 1}</b>. One unit at a time. These are timing limits, not agronomic advice – use values approved for your crop.</p>
      {cmd && (
        <div className={`note ${COMMAND_TEXT[cmd.status].tone}`} role="status">
          <Badge tone={COMMAND_TEXT[cmd.status].tone}>{COMMAND_TEXT[cmd.status].text}</Badge>
          <div className="small">Version {cmd.new_program_version} · sent {cmd.attempts}× {cmd.reason_code ? `· ${cmd.reason_code}` : ''}</div>
          {inflight && <div className="small">The controller has not confirmed. The unit keeps running its previous programme until it does.</div>}
          {cmd.status === 'UNKNOWN' && <div className="small">Do not press send again blindly – check the unit's applied version above first.</div>}
        </div>)}
      {err && <div className="note error" role="alert">{err.code === 'PROGRAM_VERSION_CONFLICT' ? `The controller already runs version ${String(err.details.actual_version)}. Review the current programme and try again.` : err.message}</div>}
      <fieldset disabled={inflight || busy}>
        <div className="grid3">
          {([['on', 'Pump on (seconds)'], ['off', 'Pump off (seconds)'], ['max', 'Max continuous on (seconds)']] as const).map(([k, l]) => (
            <label key={k}>{l}<input inputMode="numeric" value={draft[k]} onChange={e => { setDraft({ ...draft, [k]: e.target.value }); setReview(false) }} onBlur={() => setTouched(true)} aria-invalid={touched && !!errs[k]} />
              {touched && errs[k] && <span className="field-error">{errs[k]}</span>}</label>))}
        </div>
        <p className="small muted">Reservoir-level protection is always on and cannot be switched off.</p>
        {!review
          ? <button className="primary" disabled={!!blocked} onClick={() => { setTouched(true); if (!Object.keys(errs).length) setReview(true) }}>Review change</button>
          : (
            <div className="review">
              <b>Confirm:</b> pump runs {draft.on} s every {Number(draft.on) + Number(draft.off)} s (off {draft.off} s), never longer than {draft.max} s at a time, on <b>{unit.name}</b> only.
              <div><button className="primary" onClick={() => void send()} disabled={busy}>{busy ? 'Sending…' : 'Send to controller'}</button> <button onClick={() => setReview(false)}>Edit</button></div>
            </div>)}
        {blocked && <p className="field-error">{blocked}</p>}
      </fieldset>
    </section>
  )
}
