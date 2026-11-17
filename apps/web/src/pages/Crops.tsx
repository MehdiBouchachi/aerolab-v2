import { useState, type FormEvent } from 'react'
import { api, ApiError } from '../api/client'
import { useApp } from '../app/context'
import { useFetch } from '../app/hooks'
import { P } from '../api/paths'
import type { CropProfile, Range } from '../api/types'
import { label, SENSOR_LABEL } from '../app/format'
import { Empty, ErrorNote, Loading } from '../components/ui'
import { validateDraft, toProgram, type Draft } from './ProgramEditor'

// Every number on this page is typed by the grower or their agronomist. AeroLab ships no crop presets:
// fields start empty and placeholders never contain values.

interface Row { key: string; min: string; max: string }
interface Form { name: string; crop: string; variety: string; notes: string; value_source: string; rows: Row[]; withProgram: boolean; prog: Draft }

const empty = (): Form => ({ name: '', crop: '', variety: '', notes: '', value_source: '', rows: [{ key: '', min: '', max: '' }], withProgram: false, prog: { on: '', off: '', max: '' } })
const fromProfile = (p: CropProfile): Form => ({
  name: p.name, crop: p.crop, variety: p.variety ?? '', notes: p.notes ?? '', value_source: p.value_source,
  rows: Object.entries(p.thresholds).map(([key, r]) => ({ key, min: r.min?.toString() ?? '', max: r.max?.toString() ?? '' })).concat([{ key: '', min: '', max: '' }]),
  withProgram: !!p.program,
  prog: p.program ? { on: String(p.program.on_duration_seconds), off: String(p.program.off_duration_seconds), max: String(p.program.max_continuous_on_seconds) } : { on: '', off: '', max: '' },
})

/** Client-side checks only; the Pi validates again. Returns field -> message. */
export function validateForm(f: Form): Record<string, string> {
  const e: Record<string, string> = {}
  if (!f.name.trim()) e.name = 'Give the profile a name.'
  if (!f.crop.trim()) e.crop = 'Say what is grown.'
  if (!f.value_source.trim()) e.value_source = 'Say who supplied these numbers.'
  const seen = new Set<string>()
  f.rows.forEach((r, i) => {
    if (!r.key && !r.min && !r.max) return
    if (!/^[a-z][a-z0-9_]{0,39}$/.test(r.key)) e[`row${i}`] = 'Choose a reading.'
    else if (seen.has(r.key)) e[`row${i}`] = 'This reading is listed twice.'
    seen.add(r.key)
    const mn = r.min === '' ? null : Number(r.min), mx = r.max === '' ? null : Number(r.max)
    if (mn === null && mx === null) e[`row${i}`] = 'Enter a minimum, a maximum or both.'
    else if ((mn !== null && !Number.isFinite(mn)) || (mx !== null && !Number.isFinite(mx))) e[`row${i}`] = 'Numbers only.'
    else if (mn !== null && mx !== null && mn >= mx) e[`row${i}`] = 'The minimum must be below the maximum.'
  })
  if (f.withProgram) Object.entries(validateDraft(f.prog)).forEach(([k, v]) => { e[`prog_${k}`] = v! })
  return e
}

function toBody(f: Form) {
  const thresholds: Record<string, Range> = {}
  for (const r of f.rows) {
    if (!r.key) continue
    thresholds[r.key] = { ...(r.min !== '' ? { min: Number(r.min) } : {}), ...(r.max !== '' ? { max: Number(r.max) } : {}) }
  }
  return { name: f.name.trim(), crop: f.crop.trim(), variety: f.variety.trim(), notes: f.notes, value_source: f.value_source.trim(), thresholds, ...(f.withProgram ? { program: toProgram(f.prog) } : {}) }
}

function Editor({ initial, onDone, onCancel }: { initial: CropProfile | null; onDone: () => void; onCancel: () => void }) {
  const { session } = useApp(); const site = session!.site!.id
  const [f, setF] = useState<Form>(initial ? fromProfile(initial) : empty())
  const [touched, setTouched] = useState(false)
  const [err, setErr] = useState<ApiError | null>(null)
  const [busy, setBusy] = useState(false)
  const errs = validateForm(f)
  const show = (k: string) => (touched && errs[k] ? <span className="field-error" id={`err-${k}`}>{errs[k]}</span> : null)
  // the error sits OUTSIDE the label (so the field's name never changes) and is linked to the input
  const field = (k: keyof Form, text: string, extra: object = {}) => (
    <div className="field">
      <label>{text}<input value={f[k] as string} onChange={e => setF({ ...f, [k]: e.target.value })} aria-invalid={touched && !!errs[k]} aria-describedby={touched && errs[k] ? `err-${k}` : undefined} {...extra} /></label>
      {show(k)}
    </div>)
  const progField = (k: keyof Draft, text: string) => (
    <div className="field">
      <label>{text}<input inputMode="numeric" value={f.prog[k]} onChange={e => setF({ ...f, prog: { ...f.prog, [k]: e.target.value } })} aria-invalid={touched && !!errs[`prog_${k}`]} aria-describedby={touched && errs[`prog_${k}`] ? `err-prog_${k}` : undefined} /></label>
      {show(`prog_${k}`)}
    </div>)
  const setRow = (i: number, r: Partial<Row>) => {
    const rows = f.rows.map((x, j) => (j === i ? { ...x, ...r } : x))
    if (rows[rows.length - 1].key) rows.push({ key: '', min: '', max: '' }) // always one empty row to add another reading
    setF({ ...f, rows })
  }
  const save = async (e: FormEvent) => {
    e.preventDefault(); setTouched(true)
    if (Object.keys(errs).length) return
    setBusy(true); setErr(null)
    try {
      if (initial) await api('PUT', P.site(site, `/api/v1/crop-profiles/${initial.id}`), { expected_version: initial.version, ...toBody(f) })
      else await api('POST', P.site(site, `/api/v1/sites/${site}/crop-profiles`), toBody(f))
      onDone()
    } catch (x) { setErr(x as ApiError) } finally { setBusy(false) }
  }
  return (
    <form className="panel" onSubmit={save} noValidate aria-label={initial ? `Edit ${initial.name}` : 'New crop profile'}>
      <h2 style={{ marginTop: 0 }}>{initial ? `Edit “${initial.name}”` : 'New crop profile'}</h2>
      <p className="muted small">Enter the numbers your agronomist, seed supplier or your own trials give for this crop. AeroLab does not suggest values. Ranges raise advisory alerts on the Pi; they never switch the pump.</p>
      <ErrorNote error={err} />
      {err?.code === 'VERSION_CONFLICT' && <p className="note warn">Someone saved this profile after you opened it. Close the editor and open it again to see their changes.</p>}
      <div className="grid3">
        {field('name', 'Profile name', { maxLength: 80 })}
        {field('crop', 'Crop', { maxLength: 80 })}
        {field('variety', 'Variety (optional)', { maxLength: 80 })}
      </div>
      {field('value_source', 'Who supplied these numbers?', { maxLength: 200 })}
      <h3 style={{ marginTop: '1rem' }}>Reading ranges</h3>
      <div className="table-wrap"><table className="readings ranges"><thead><tr><th>Reading</th><th>Minimum</th><th>Maximum</th></tr></thead><tbody>
        {f.rows.map((r, i) => (
          <tr key={i}>
            <td><select aria-label={`Reading ${i + 1}`} value={r.key} onChange={e => setRow(i, { key: e.target.value })}>
              <option value="">{i === f.rows.length - 1 ? 'Add a reading…' : 'Choose…'}</option>
              {Object.keys(SENSOR_LABEL).filter(k => k !== 'reservoir_level_ok').map(k => <option key={k} value={k}>{label(k)}</option>)}
              {r.key && !(r.key in SENSOR_LABEL) && <option value={r.key}>{r.key}</option>}
            </select>{show(`row${i}`)}</td>
            <td><input inputMode="decimal" aria-label={`Minimum ${label(r.key || 'reading')}`} value={r.min} onChange={e => setRow(i, { min: e.target.value })} /></td>
            <td><input inputMode="decimal" aria-label={`Maximum ${label(r.key || 'reading')}`} value={r.max} onChange={e => setRow(i, { max: e.target.value })} /></td>
          </tr>))}
      </tbody></table></div>
      <label style={{ flexDirection: 'row', gap: '.5rem', alignItems: 'center', marginTop: '1rem' }}>
        <input type="checkbox" checked={f.withProgram} onChange={e => setF({ ...f, withProgram: e.target.checked })} style={{ minHeight: 0 }} /> Include an irrigation programme for this crop
      </label>
      {f.withProgram && (<div className="grid3">
        {progField('on', 'Pump on (seconds)')}
        {progField('off', 'Pump off (seconds)')}
        {progField('max', 'Longest continuous pump-on (seconds)')}
        <p className="muted small" style={{ gridColumn: '1/-1' }}>The controller still enforces its own hard limit and the reservoir interlock. Assigning this profile does not send the programme; you apply it from the unit page and the controller must confirm it.</p>
      </div>)}
      <label>Notes (optional)<textarea rows={3} value={f.notes} onChange={e => setF({ ...f, notes: e.target.value })} /></label>
      <div style={{ display: 'flex', gap: '.5rem', marginTop: '.75rem' }}>
        <button className="primary" disabled={busy}>{busy ? 'Saving…' : initial ? 'Save changes' : 'Create profile'}</button>
        <button type="button" onClick={onCancel}>Cancel</button>
      </div>
    </form>
  )
}

export function Crops() {
  const { session, can } = useApp(); const site = session!.site!.id
  const list = useFetch<{ items: CropProfile[] }>(P.site(site, `/api/v1/sites/${site}/crop-profiles`), ['crop_profile.updated', 'unit.updated'])
  const [editing, setEditing] = useState<CropProfile | 'new' | null>(null)
  const [err, setErr] = useState<ApiError | null>(null)
  const edit = can('edit_program')
  const archive = async (p: CropProfile) => {
    if (!window.confirm(`Archive the crop profile “${p.name}”? Units keep their history.`)) return
    setErr(null)
    try { await api('DELETE', P.site(site, `/api/v1/crop-profiles/${p.id}`)); list.reload() } catch (x) { setErr(x as ApiError) }
  }
  return (
    <>
      <h1>Crop profiles</h1>
      <p className="muted">A crop profile holds the ranges (and optionally the irrigation programme) for one crop. Give each growing unit the profile of what grows in it, so a tomato unit and a lettuce unit are each watched against their own numbers.</p>
      <ErrorNote error={err ?? list.error} retry={list.reload} />
      {edit && !editing && <button className="primary" onClick={() => setEditing('new')}>New crop profile</button>}
      {editing && <Editor initial={editing === 'new' ? null : editing} onCancel={() => setEditing(null)} onDone={() => { setEditing(null); list.reload() }} />}
      {list.loading && <Loading what="crop profiles" />}
      {list.data && list.data.items.length === 0 && !editing && <Empty>No crop profiles yet.{edit ? ' Create one with the numbers for what you grow.' : ' An administrator can create them.'}</Empty>}
      <div className="cards" style={{ marginTop: '1rem' }}>
        {list.data?.items.map(p => (
          <article key={p.id} className="card">
            <header><h3>{p.name}</h3><span className="muted small">v{p.version}</span></header>
            <div><b>{p.crop}</b>{p.variety && ` · ${p.variety}`}</div>
            <dl>{Object.entries(p.thresholds).map(([k, r]) => <div className="row" key={k}><dt>{label(k)}</dt><dd>{r.min ?? '–'} to {r.max ?? '–'}</dd></div>)}
              {p.program && <div className="row"><dt>Programme</dt><dd>on {p.program.on_duration_seconds} s / off {p.program.off_duration_seconds} s</dd></div>}</dl>
            {Object.keys(p.thresholds).length === 0 && <p className="muted small">No ranges set: no range alerts for units with this profile.</p>}
            <p className="muted small">Numbers from: {p.value_source} · used by {p.unit_count} unit{p.unit_count === 1 ? '' : 's'}</p>
            {edit && <div style={{ display: 'flex', gap: '.5rem' }}><button onClick={() => setEditing(p)}>Edit {p.name}</button>{p.unit_count === 0 && <button onClick={() => void archive(p)}>Archive</button>}</div>}
          </article>))}
      </div>
    </>
  )
}
