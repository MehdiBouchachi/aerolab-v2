import { Tr } from '../app/language'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import QRCode from 'qrcode'
import { api, ApiError } from '../api/client'
import { useApp } from '../app/context'
import { useFetch } from '../app/hooks'
import type { CropProfile, SetupBundle } from '../api/types'
import { ErrorNote } from '../components/ui'
import { HOME, P } from '../api/paths'

/** Shows a controller setup code exactly once, as QR + text, with what to do next. */
export function SetupCode({ b }: { b: SetupBundle }) {
  const [qr, setQr] = useState('')
  const [copied, setCopied] = useState(false)
  useEffect(() => { void QRCode.toDataURL(b.setup_code, { margin: 1, width: 300, errorCorrectionLevel: 'M' }).then(setQr) }, [b.setup_code])
  const copy = async () => { try { await navigator.clipboard.writeText(b.setup_code); setCopied(true) } catch { setCopied(false) } }
  return (
    <section className="panel" aria-label="Controller setup code">
      <h2 style={{ marginTop: 0 }}><Tr>Set up the controller</Tr></h2>
      <p className="note warn"><b>Shown once.</b> {b.shown_once}</p>
      {b.warning && <p className="note warn">{b.warning}</p>}
      {b.note && <p className="note">{b.note}</p>}
      <ol>
        <li>Power the new controller. While it has no setup, it opens a Wi-Fi network named <b>AeroLab-Setup-…</b>.</li>
        <li>Join that network with your phone and open <b>http://192.168.4.1</b>.</li>
        <li>Enter your farm Wi-Fi name and password, and paste this setup code (or scan the QR code). The controller restarts and appears here within a minute.</li>
      </ol>
      {qr && <img src={qr} width={300} height={300} alt="Controller setup code as a QR code" />}
      <label><Tr>Setup code</Tr><textarea readOnly rows={3} value={b.setup_code} onFocus={e => e.currentTarget.select()} /></label>
      <button type="button" onClick={() => void copy()}>{copied ? 'Copied' : <Tr>{"Copy setup code"}</Tr>}</button>
      <p className="muted small">Controller {b.setup.device_id} · connects to {b.setup.mqtt_uri}. If your controller cannot reach that address, set AEROLAB_BROKER_PUBLIC_URL on the Pi to an address it can reach.</p>
    </section>
  )
}

export function AddUnit() {
  const { session, can } = useApp(); const site = session!.site!.id
  const navigate = useNavigate()
  const crops = useFetch<{ items: CropProfile[] }>(P.site(site, `/api/v1/sites/${site}/crop-profiles`), [], 0)
  const [name, setName] = useState('')
  const [crop, setCrop] = useState('')
  const [touched, setTouched] = useState(false)
  const [err, setErr] = useState<ApiError | null>(null)
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState<SetupBundle | null>(null)
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const element = dialog.current
    element?.showModal()
    return () => element?.close()
  }, [])
  const close = () => navigate(HOME, { replace: true })
  const allowed = can('manage_devices')
  const submit = async (e: FormEvent) => {
    e.preventDefault(); setTouched(true)
    if (busy || done || !allowed || !name.trim()) return
    setBusy(true); setErr(null)
    try { setDone(await api<SetupBundle>('POST', P.site(site, `/api/v1/sites/${site}/units`), { name: name.trim(), ...(crop ? { crop_profile_id: crop } : {}) })) } catch (x) { setErr(x as ApiError) } finally { setBusy(false) }
  }
  return (
    <dialog ref={dialog} className={`app-modal-dialog add-unit-dialog${done ? '' : ' unit-action-dialog'}`} aria-labelledby="add-unit-title"
      onCancel={event => { event.preventDefault(); if (!busy) close() }}>
      <form className="app-modal-form" onSubmit={submit} noValidate>
        <header className="app-modal-header"><h2 id="add-unit-title">{done ? <Tr>{"Unit added"}</Tr> : <Tr>{"Add a growing unit"}</Tr>}</h2></header>
        <div className="app-modal-body add-unit-fields">
        {!allowed ? <p>Only an administrator can add units.</p> : done ? <SetupCode b={done} /> : <>
          <ErrorNote error={err} />
          {err?.code === 'BROKER_NOT_MANAGED' && <p className="note warn">This Pi was installed without self-service units. Ask your installer to enable it (AEROLAB_BROKER_PASSWD_FILE).</p>}
          <label><Tr>Unit name</Tr><input value={name} onChange={e => setName(e.target.value)} maxLength={80} aria-invalid={touched && !name.trim()} aria-describedby={touched && !name.trim() ? 'add-unit-name-error' : undefined} autoFocus /></label>
          {touched && !name.trim() && <span id="add-unit-name-error" className="field-error">Enter a unit name.</span>}
          <label><Tr>Crop profile (optional)</Tr>
            <select value={crop} onChange={e => setCrop(e.target.value)} disabled={!crops.data}>
              <option value="">Decide later</option>
              {crops.data?.items.map(p => <option key={p.id} value={p.id}>{p.crop} – {p.name}</option>)}
            </select></label>
          {crops.data && crops.data.items.length === 0 && <p className="muted small">No crop profiles yet. <Link to="/crops">Create one</Link> to get alerts tuned to the crop.</p>}
          <ErrorNote error={crops.error} retry={crops.reload} />
        </>}
        </div>
        <footer className="app-modal-footer">
          <button type="button" onClick={close} disabled={busy}>{done || !allowed ? <Tr>{"Close"}</Tr> : <Tr>{"Cancel"}</Tr>}</button>
          {done ? <Link className="btn" to={`/units/${done.setup.unit_id}`} replace><Tr>Open unit</Tr></Link> : allowed && <button className="primary" disabled={busy}>{busy ? 'Adding…' : <Tr>{"Add unit"}</Tr>}</button>}
        </footer>
      </form>
    </dialog>
  )
}
