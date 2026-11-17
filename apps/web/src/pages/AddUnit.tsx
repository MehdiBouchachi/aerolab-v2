import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
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
      <h2 style={{ marginTop: 0 }}>Set up the controller</h2>
      <p className="note warn"><b>Shown once.</b> {b.shown_once}</p>
      {b.warning && <p className="note warn">{b.warning}</p>}
      {b.note && <p className="note">{b.note}</p>}
      <ol>
        <li>Power the new controller. While it has no setup, it opens a Wi-Fi network named <b>AeroLab-Setup-…</b>.</li>
        <li>Join that network with your phone and open <b>http://192.168.4.1</b>.</li>
        <li>Enter your farm Wi-Fi name and password, and paste this setup code (or scan the QR code). The controller restarts and appears here within a minute.</li>
      </ol>
      {qr && <img src={qr} width={300} height={300} alt="Controller setup code as a QR code" />}
      <label>Setup code<textarea readOnly rows={3} value={b.setup_code} onFocus={e => e.currentTarget.select()} /></label>
      <button type="button" onClick={() => void copy()}>{copied ? 'Copied' : 'Copy setup code'}</button>
      <p className="muted small">Controller {b.setup.device_id} · connects to {b.setup.mqtt_uri}. If your controller cannot reach that address, set AEROLAB_BROKER_PUBLIC_URL on the Pi to an address it can reach.</p>
    </section>
  )
}

export function AddUnit() {
  const { session, can } = useApp(); const site = session!.site!.id
  const crops = useFetch<{ items: CropProfile[] }>(P.site(site, `/api/v1/sites/${site}/crop-profiles`), [], 0)
  const [name, setName] = useState('')
  const [crop, setCrop] = useState('')
  const [touched, setTouched] = useState(false)
  const [err, setErr] = useState<ApiError | null>(null)
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState<SetupBundle | null>(null)
  if (!can('manage_devices')) return <p>Only an administrator can add units.</p>
  const submit = async (e: FormEvent) => {
    e.preventDefault(); setTouched(true)
    if (!name.trim()) return
    setBusy(true); setErr(null)
    try { setDone(await api<SetupBundle>('POST', P.site(site, `/api/v1/sites/${site}/units`), { name: name.trim(), ...(crop ? { crop_profile_id: crop } : {}) })) } catch (x) { setErr(x as ApiError) } finally { setBusy(false) }
  }
  return (
    <>
      <p><Link to={HOME}>← Overview</Link></p>
      <h1>Add a growing unit</h1>
      {!done ? (
        <form className="panel" onSubmit={submit} noValidate aria-label="Add a growing unit">
          <p className="muted">The Pi creates the controller's identity and password. No technician or configuration file is needed.</p>
          <ErrorNote error={err} />
          {err?.code === 'BROKER_NOT_MANAGED' && <p className="note warn">This Pi was installed without self-service units. Ask your installer to enable it (AEROLAB_BROKER_PASSWD_FILE).</p>}
          <label>Unit name<input value={name} onChange={e => setName(e.target.value)} maxLength={80} aria-invalid={touched && !name.trim()} autoFocus /></label>
          {touched && !name.trim() && <span className="field-error">Give the unit a name you will recognise, e.g. where it stands.</span>}
          <label>What grows in it? (crop profile, optional)
            <select value={crop} onChange={e => setCrop(e.target.value)}>
              <option value="">Decide later</option>
              {crops.data?.items.map(p => <option key={p.id} value={p.id}>{p.crop} – {p.name}</option>)}
            </select></label>
          {crops.data && crops.data.items.length === 0 && <p className="muted small">No crop profiles yet. <Link to="/crops">Create one</Link> to get alerts tuned to the crop.</p>}
          <button className="primary" disabled={busy} style={{ marginTop: '.75rem' }}>{busy ? 'Adding…' : 'Add unit'}</button>
        </form>
      ) : (<>
        <p className="note ok">Unit <b>{done.unit?.name}</b> added.</p>
        <SetupCode b={done} />
        <p><Link to={`/units/${done.setup.unit_id}`}>Open the unit</Link> · <Link to={HOME}>Back to the overview</Link></p>
      </>)}
    </>
  )
}
