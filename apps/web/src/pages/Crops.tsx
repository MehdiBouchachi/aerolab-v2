import { Tr, useTranslation } from '../app/language'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { api, ApiError } from '../api/client'
import { useApp } from '../app/context'
import { useFetch } from '../app/hooks'
import { P } from '../api/paths'
import type { CropProfile, Range } from '../api/types'
import { label, SENSOR_LABEL } from '../app/format'
import { Empty, ErrorNote, Loading } from '../components/ui'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { validateDraft, toProgram, type Draft } from './ProgramEditor'

// Every number on this page is typed by the grower or their agronomist. AeroLab ships no crop presets:
// fields start empty and placeholders never contain values.

interface Row { key: string; min: string; max: string }
interface Form { name: string; crop: string; variety: string; notes: string; value_source: string; rows: Row[]; withProgram: boolean; prog: Draft }

const empty = (): Form => ({ name: '', crop: '', variety: '', notes: '', value_source: '', rows: [], withProgram: false, prog: { on: '', off: '', max: '' } })
const fromProfile = (p: CropProfile): Form => ({
  name: p.name, crop: p.crop, variety: p.variety ?? '', notes: p.notes ?? '', value_source: p.value_source,
  rows: Object.entries(p.thresholds).map(([key, r]) => ({ key, min: r.min?.toString() ?? '', max: r.max?.toString() ?? '' })),
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
    if (seen.has(r.key)) {
      e[`row${i}`] = 'This sensor is already listed.'
      return
    }
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
  const availableSensors = Object.keys(SENSOR_LABEL).filter(key => key !== 'reservoir_level_ok' && !f.rows.some(row => row.key === key))
  const addSensor = (key: string) => {
    if (!availableSensors.includes(key)) return
    setF(previous => previous.rows.some(row => row.key === key) ? previous : {
      ...previous, rows: [...previous.rows, { key, min: '', max: '' }],
    })
  }
  const setRange = (key: string, bound: 'min' | 'max', value: string) => {
    setF(previous => ({ ...previous, rows: previous.rows.map(row => row.key === key ? { ...row, [bound]: value } : row) }))
  }
  const removeSensor = (key: string) => {
    setF(previous => ({ ...previous, rows: previous.rows.filter(row => row.key !== key) }))
  }
  const save = async (e: FormEvent) => {
    e.preventDefault(); setTouched(true)
    if (busy || Object.keys(errs).length) return
    setBusy(true); setErr(null)
    try {
      if (initial) await api('PUT', P.site(site, `/api/v1/crop-profiles/${initial.id}`), { expected_version: initial.version, ...toBody(f) })
      else await api('POST', P.site(site, `/api/v1/sites/${site}/crop-profiles`), toBody(f))
      onDone()
    } catch (x) { setErr(x as ApiError) } finally { setBusy(false) }
  }
  const dialogRef = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const dialog = dialogRef.current
    dialog?.showModal()
    return () => dialog?.close()
  }, [])
  return (
    <dialog ref={dialogRef} className="app-modal-dialog" aria-label={initial ? `Edit ${initial.name}` : 'New crop profile'} onCancel={event => { event.preventDefault(); if (!busy) onCancel() }}>
    <form className="app-modal-form" onSubmit={save} noValidate>
      <header className="app-modal-header"><h2>{initial ? `Edit “${initial.name}”` : <Tr>{"New crop profile"}</Tr>}</h2></header>
      <div className="app-modal-body">
      <ErrorNote error={err} />
      {err?.code === 'VERSION_CONFLICT' && <p className="note warn">Someone saved this profile after you opened it. Close the editor and open it again to see their changes.</p>}
      <section className="crop-editor-section" aria-label="Profile details">
      <div className="grid3">
        {field('name', 'Profile name', { maxLength: 80, autoFocus: true })}
        {field('crop', 'Crop', { maxLength: 80 })}
        {field('variety', 'Variety (optional)', { maxLength: 80 })}
      </div>
      {field('value_source', 'Range source', { maxLength: 200, placeholder: 'Agronomist, supplier, or trial' })}
      </section>
      <section className="crop-editor-section" aria-labelledby="crop-ranges-title">
      <h3 id="crop-ranges-title"><Tr>Sensor ranges</Tr></h3>
      <p className="muted small crop-range-help">Ranges trigger alerts only.</p>
      {f.rows.length > 0 && <ul className="crop-sensor-list">
        {f.rows.map((row, i) => {
          const errorId = `row${i}`
          return (
            <li className="crop-sensor-row" key={row.key}>
              <strong>{label(row.key)}</strong>
              <label><Tr>Min</Tr><input inputMode="decimal" aria-label={`Minimum ${label(row.key)}`} aria-invalid={touched && !!errs[errorId]} aria-describedby={touched && errs[errorId] ? `err-${errorId}` : undefined} value={row.min} onChange={e => setRange(row.key, 'min', e.target.value)} /></label>
              <label><Tr>Max</Tr><input inputMode="decimal" aria-label={`Maximum ${label(row.key)}`} aria-invalid={touched && !!errs[errorId]} aria-describedby={touched && errs[errorId] ? `err-${errorId}` : undefined} value={row.max} onChange={e => setRange(row.key, 'max', e.target.value)} /></label>
              <button type="button" className="crop-remove-sensor" aria-label={`Remove ${label(row.key)}`} onClick={() => removeSensor(row.key)}><Tr>Remove</Tr></button>
              {show(errorId)}
            </li>
          )
        })}
      </ul>}
      {availableSensors.length > 0 && <select className="crop-add-sensor" aria-label="Add sensor" value="" onChange={e => addSensor(e.target.value)}>
        <option value="">Add sensor</option>
        {availableSensors.map(key => <option key={key} value={key}>{label(key)}</option>)}
      </select>}
      </section>
      <section className="crop-editor-section" aria-label="Optional settings">
      <label className="crop-program-toggle">
        <input type="checkbox" checked={f.withProgram} onChange={e => setF({ ...f, withProgram: e.target.checked })} /> <Tr>Include irrigation programme</Tr>
      </label>
      {f.withProgram && (<div className="grid3">
        {progField('on', 'Pump on (s)')}
        {progField('off', 'Pump off (s)')}
        {progField('max', 'Max continuous on (s)')}
        <p className="muted small crop-program-help">Apply from the unit page. Controller limits and reservoir protection still apply.</p>
      </div>)}
      <details className="crop-editor-notes">
        <summary><Tr>Notes (optional)</Tr></summary>
        <textarea aria-label="Notes" rows={3} value={f.notes} onChange={e => setF({ ...f, notes: e.target.value })} />
      </details>
      </section>
      </div>
      <div className="app-modal-footer">
        <button type="button" onClick={onCancel} disabled={busy}><Tr>Cancel</Tr></button>
        <button className="primary" disabled={busy}>{busy ? 'Saving…' : initial ? <Tr>{"Save changes"}</Tr> : <Tr>{"Create profile"}</Tr>}</button>
      </div>
    </form>
    </dialog>
  )
}

export function Crops() {
  const t = useTranslation()
  const { session, can } = useApp(); const site = session!.site!.id
  const list = useFetch<{ items: CropProfile[] }>(P.site(site, `/api/v1/sites/${site}/crop-profiles`), ['crop_profile.updated', 'unit.updated'])
  const [editing, setEditing] = useState<CropProfile | 'new' | null>(null)
  const [archiving, setArchiving] = useState<CropProfile | null>(null)
  const edit = can('edit_program')
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)
  const pageSize = 12
  const items = list.data?.items ?? []
  const matches = items.filter(p => [p.name, p.crop, p.variety].some(value => value?.toLowerCase().includes(query.trim().toLowerCase())))
  const pages = Math.max(1, Math.ceil(matches.length / pageSize))
  const current = Math.min(page, pages)
  const start = (current - 1) * pageSize
  const archive = async (p: CropProfile) => {
    await api('DELETE', P.site(site, `/api/v1/crop-profiles/${p.id}`))
    list.reload()
  }
  return (
    <>
      <header className="crop-heading">
        <div>
          <h1><Tr>Crop profiles</Tr></h1>
          <p className="muted"><Tr>Manage sensor ranges and irrigation programmes for your crops.</Tr></p>
        </div>
        {edit && <button className="primary" onClick={() => setEditing('new')}><Tr>New crop profile</Tr></button>}
      </header>
      <ErrorNote error={list.error} retry={list.reload} />
      {archiving && <ConfirmDialog title={`Archive ${archiving.name}?`} description="Units keep their history." confirmLabel="Archive profile" onConfirm={() => archive(archiving)} onClose={() => setArchiving(null)} />}
      {editing && <Editor initial={editing === 'new' ? null : editing} onCancel={() => setEditing(null)} onDone={() => { setEditing(null); list.reload() }} />}
      {list.loading && <Loading what="crop profiles" />}
      {list.data && list.data.items.length === 0 && !editing && <Empty>No crop profiles yet.{edit ? ' Create one with the numbers for what you grow.' : ' An administrator can create them.'}</Empty>}
      {items.length > 0 && <section className="crop-directory" aria-label="Crop profile directory">
        <div className="crop-toolbar">
          <input aria-label={t('Search crop profiles')} type="search" value={query} onChange={e => { setQuery(e.target.value); setPage(1) }} placeholder={t('Search crop profiles')} />
          {query && <button onClick={e => { setQuery(''); setPage(1); (e.currentTarget.previousElementSibling as HTMLInputElement | null)?.focus() }}><Tr>Clear search</Tr></button>}
        </div>
        {matches.length === 0 ? <Empty>No profiles match your search.</Empty> : <>
          <div className="table-wrap crop-table-wrap" tabIndex={0} role="region" aria-label="Crop profiles table">
            <table className="crop-table">
              <thead><tr><th scope="col"><Tr>Profile</Tr></th><th scope="col"><Tr>Crop</Tr></th><th scope="col"><Tr>Units</Tr></th><th scope="col"><Tr>Details</Tr></th>{edit && <th scope="col"><Tr>Actions</Tr></th>}</tr></thead>
              <tbody>{matches.slice(start, start + pageSize).map(p => (
                <tr key={p.id}>
                  <th scope="row">{p.name}<small className="muted">Version {p.version}</small></th>
                  <td>{p.crop}{p.variety && <small className="muted">{p.variety}</small>}</td>
                  <td>{p.unit_count}</td>
                  <td><details>
                    <summary aria-label={`Ranges and programme for ${p.name}`}><Tr>Ranges and programme</Tr></summary>
                    <dl>{Object.entries(p.thresholds).map(([k, r]) => <div className="row" key={k}><dt><Tr>{label(k)}</Tr></dt><dd>{r.min ?? '–'} to {r.max ?? '–'}</dd></div>)}
                      <div className="row"><dt><Tr>Programme</Tr></dt><dd>{p.program ? `on ${p.program.on_duration_seconds} s / off ${p.program.off_duration_seconds} s` : <Tr>{"None"}</Tr>}</dd></div>
                    </dl>
                    {Object.keys(p.thresholds).length === 0 && <p className="muted">No ranges set. Units with this profile have no range alerts.</p>}
                    <p className="muted">Numbers from: {p.value_source}</p>
                  </details></td>
                  {edit && <td><div className="crop-actions"><button aria-label={`Edit ${p.name}`} onClick={() => setEditing(p)}><Tr>Edit</Tr></button>{p.unit_count === 0 && <button aria-label={`Archive ${p.name}`} onClick={() => setArchiving(p)}><Tr>Archive</Tr></button>}</div></td>}
                </tr>
              ))}</tbody>
            </table>
          </div>
          <div className="crop-pagination">
            <span className="muted" role="status">{pages > 1 ? `${start + 1}–${Math.min(start + pageSize, matches.length)} of ${matches.length} profiles` : `${matches.length} ${matches.length === 1 ? 'profile' : 'profiles'}`}</span>
            {pages > 1 && <div className="crop-actions"><button disabled={current === 1} onClick={() => setPage(current - 1)}><Tr>Previous</Tr></button><button disabled={current === pages} onClick={() => setPage(current + 1)}><Tr>Next</Tr></button></div>}
          </div>
        </>}
      </section>}
    </>
  )
}
