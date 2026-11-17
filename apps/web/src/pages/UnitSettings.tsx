import { useState } from 'react'
import { api, ApiError } from '../api/client'
import { useApp } from '../app/context'
import { useFetch } from '../app/hooks'
import type { Command, CropProfile, SetupBundle, Unit } from '../api/types'
import { IS_CLOUD, P } from '../api/paths'
import { ErrorNote } from '../components/ui'
import { SetupCode } from './AddUnit'

/** Local-only unit administration: name, crop profile, controller setup code, retirement. */
export function UnitSettings({ unit, onChanged, onGone }: { unit: Unit; onChanged: () => void; onGone: () => void }) {
  const { session, can } = useApp(); const site = session!.site!.id
  const crops = useFetch<{ items: CropProfile[] }>(P.site(site, `/api/v1/sites/${site}/crop-profiles`), ['crop_profile.updated'], 0)
  const [name, setName] = useState(unit.name)
  const [err, setErr] = useState<ApiError | null>(null)
  const [msg, setMsg] = useState('')
  const [bundle, setBundle] = useState<SetupBundle | null>(null)
  const [confirm, setConfirm] = useState('')
  const [retiring, setRetiring] = useState(false)
  const run = async (f: () => Promise<unknown>, ok: string) => { setErr(null); setMsg(''); try { await f(); setMsg(ok); onChanged() } catch (x) { setErr(x as ApiError) } }
  const profile = crops.data?.items.find(p => p.id === unit.crop?.profile_id)
  const applyProgram = () => run(async () => {
    await api<Command>('POST', P.programRequest(site, unit.id), { ...(IS_CLOUD ? { unit_id: unit.id } : {}), command_id: crypto.randomUUID(), expected_program_version: unit.applied_program_version ?? 0, program: profile!.program })
  }, 'Programme sent. It counts as applied only when the controller confirms it (see the programme section).')

  return (
    <section aria-label="Unit settings">
      <h2>Unit settings</h2>
      <ErrorNote error={err} />
      {msg && <p className="note ok" role="status">{msg}</p>}
      <div className="panel">
        {can('manage_devices') && (
          <form className="inline-form" onSubmit={e => { e.preventDefault(); void run(() => api('PATCH', P.site(site, `/api/v1/units/${unit.id}`), { name: name.trim() }), 'Name saved.') }}>
            <label>Name<input value={name} onChange={e => setName(e.target.value)} maxLength={80} /></label>
            <button disabled={!name.trim() || name.trim() === unit.name}>Rename</button>
          </form>)}
        <div className="inline-form">
          <label>Crop profile
            <select value={unit.crop?.profile_id ?? ''} disabled={!can('edit_program')}
              onChange={e => void run(() => api('PUT', P.site(site, `/api/v1/units/${unit.id}/crop-profile`), { crop_profile_id: e.target.value }), e.target.value ? 'Crop profile assigned. Alerts now use its ranges.' : 'Crop profile removed.')}>
              <option value="">None</option>
              {crops.data?.items.map(p => <option key={p.id} value={p.id}>{p.crop} – {p.name}</option>)}
            </select></label>
          {can('edit_program') && profile?.program && (
            <button type="button" disabled={unit.connection !== 'ONLINE'} title={unit.connection !== 'ONLINE' ? 'The controller is not reporting' : undefined} onClick={() => void applyProgram()}>
              Send this profile's programme (on {profile.program.on_duration_seconds} s / off {profile.program.off_duration_seconds} s)
            </button>)}
        </div>
      </div>

      {can('manage_devices') && (<>
        <h3 style={{ marginTop: '1rem' }}>Controller</h3>
        <p className="muted small">Lost the setup code, or replacing the controller board? A new code replaces the old password; a controller already running must then be set up again.</p>
        <button onClick={() => { if (window.confirm('Create a new setup code? The current controller password stops working.')) void run(async () => setBundle(await api<SetupBundle>('POST', P.site(site, `/api/v1/units/${unit.id}/setup-code`))), 'New setup code created.') }}>Create a new setup code</button>
        {bundle && <div style={{ marginTop: '.75rem' }}><SetupCode b={bundle} /></div>}

        <h3 style={{ marginTop: '1rem' }}>Retire this unit</h3>
        {!retiring ? <button onClick={() => setRetiring(true)}>Retire {unit.name}…</button> : (
          <form className="panel" onSubmit={e => { e.preventDefault(); void run(async () => { await api('POST', P.site(site, `/api/v1/units/${unit.id}/retire`), { confirm_name: confirm }); onGone() }, 'Unit retired.') }}>
            <p>Retiring removes the unit from the lists and locks its controller out of the Pi. Its history is kept. This cannot be undone from the dashboard.</p>
            <label>Type the unit name <b>{unit.name}</b> to confirm<input value={confirm} onChange={e => setConfirm(e.target.value)} autoComplete="off" /></label>
            <div style={{ display: 'flex', gap: '.5rem', marginTop: '.5rem' }}>
              <button className="primary" disabled={confirm !== unit.name}>Retire {unit.name}</button>
              <button type="button" onClick={() => { setRetiring(false); setConfirm('') }}>Cancel</button>
            </div>
          </form>)}
      </>)}
    </section>
  )
}
