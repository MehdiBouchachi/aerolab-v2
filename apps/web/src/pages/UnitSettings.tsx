import { useEffect, useRef, useState } from 'react'
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
  const [action, setAction] = useState<'setup' | 'retire' | null>(null)
  const [actionError, setActionError] = useState<ApiError | null>(null)
  const [busy, setBusy] = useState(false)
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    if (!action) return
    const element = dialog.current
    element?.showModal()
    return () => element?.close()
  }, [action])
  const closeAction = () => {
    setAction(null)
    setActionError(null)
    setConfirm('')
  }
  const confirmAction = async () => {
    if (busy || !action || (action === 'retire' && confirm !== unit.name)) return
    setBusy(true)
    setActionError(null)
    try {
      if (action === 'setup') {
        setBundle(await api<SetupBundle>('POST', P.site(site, `/api/v1/units/${unit.id}/setup-code`)))
        setErr(null)
        setMsg('New setup code created.')
        onChanged()
      } else {
        await api('POST', P.site(site, `/api/v1/units/${unit.id}/retire`), { confirm_name: confirm })
        onGone()
      }
      closeAction()
    } catch (error) {
      setActionError(error as ApiError)
    } finally {
      setBusy(false)
    }
  }
  const run = async (f: () => Promise<unknown>, ok: string) => { setErr(null); setMsg(''); try { await f(); setMsg(ok); onChanged() } catch (x) { setErr(x as ApiError) } }
  const profile = crops.data?.items.find(p => p.id === unit.crop?.profile_id)
  const applyProgram = () => run(async () => {
    await api<Command>('POST', P.programRequest(site, unit.id), { ...(IS_CLOUD ? { unit_id: unit.id } : {}), command_id: crypto.randomUUID(), expected_program_version: unit.applied_program_version ?? 0, program: profile!.program })
  }, 'Programme sent. It counts as applied only when the controller confirms it (see the programme section).')

  return (
    <section className="unit-settings" aria-labelledby="unit-settings-title">
      <h2 id="unit-settings-title">Unit settings</h2>
      <ErrorNote error={err} />
      {msg && <p className="note ok" role="status">{msg}</p>}
      <div className="unit-settings-surface">
        {can('manage_devices') && (
          <form className="unit-setting-row" noValidate onSubmit={e => { e.preventDefault(); if (name.trim() && name.trim() !== unit.name) void run(() => api('PATCH', P.site(site, `/api/v1/units/${unit.id}`), { name: name.trim() }), 'Name saved.') }}>
            <label htmlFor="unit-settings-name">Unit name</label>
            <div className="unit-setting-control">
              <input id="unit-settings-name" value={name} onChange={e => setName(e.target.value)} maxLength={80} />
              <button disabled={!name.trim() || name.trim() === unit.name}>Save name</button>
            </div>
          </form>)}
        <div className="unit-setting-row">
          <div><label htmlFor="unit-settings-crop">Crop profile</label><p className="muted small">Sets the sensor ranges used for alerts.</p></div>
          <div className="unit-setting-crop">
            <select id="unit-settings-crop" value={unit.crop?.profile_id ?? ''} disabled={!can('edit_program') || !crops.data}
              onChange={e => void run(() => api('PUT', P.site(site, `/api/v1/units/${unit.id}/crop-profile`), { crop_profile_id: e.target.value }), e.target.value ? 'Crop profile assigned. Alerts now use its ranges.' : 'Crop profile removed.')}>
              <option value="">No crop profile</option>
              {crops.data?.items.map(p => <option key={p.id} value={p.id}>{p.crop} – {p.name}</option>)}
            </select>
          <ErrorNote error={crops.error} retry={crops.reload} />
          {can('edit_program') && profile?.program && (
            <button type="button" disabled={unit.connection !== 'ONLINE'} title={unit.connection !== 'ONLINE' ? 'The controller is not reporting' : undefined} onClick={() => void applyProgram()}>
              Send this profile's programme (on {profile.program.on_duration_seconds} s / off {profile.program.off_duration_seconds} s)
            </button>)}
          </div>
        </div>
      </div>

      {can('manage_devices') && (<>
        <section className="unit-settings-surface unit-setting-row">
        <div><h3>Controller setup</h3>
        <p className="muted small">A new code replaces the controller password. Set up the controller again to reconnect it.</p></div>
        <button onClick={() => setAction('setup')}>Create a new setup code</button>
        {bundle && <div className="unit-setting-expanded"><SetupCode b={bundle} /></div>}
        </section>

        <section className="unit-settings-surface unit-setting-row">
        <div><h3>Retire unit</h3><p className="muted small">Removes the unit from lists and locks out its controller. History is kept.</p></div>
        <button className="unit-retire-action" onClick={() => setAction('retire')}>Retire unit</button>
        </section>
        {action && <dialog ref={dialog} className="app-modal-dialog unit-action-dialog"
          aria-labelledby="unit-action-title" aria-describedby="unit-action-description"
          onCancel={event => { event.preventDefault(); if (!busy) closeAction() }}>
          <form className="app-modal-form" noValidate onSubmit={event => { event.preventDefault(); void confirmAction() }}>
            <header className="app-modal-header"><h2 id="unit-action-title">{action === 'setup' ? 'Create a new setup code?' : 'Retire this unit?'}</h2></header>
            <div className="app-modal-body">
              <p id="unit-action-description" className="muted">{action === 'setup'
                ? 'The current controller password will stop working. Use the new code to set up the controller again.'
                : 'This removes the unit from lists and locks out its controller. History is kept. This cannot be undone from the dashboard.'}</p>
              {action === 'retire' && <label>Type <b>{unit.name}</b> to confirm<input value={confirm} onChange={event => setConfirm(event.target.value)} autoComplete="off" disabled={busy} /></label>}
              <ErrorNote error={actionError} />
            </div>
            <footer className="app-modal-footer">
              <button type="button" autoFocus onClick={closeAction} disabled={busy}>Cancel</button>
              <button className={action === 'setup' ? 'primary' : 'unit-retire-action'} disabled={busy || (action === 'retire' && confirm !== unit.name)}>
                {action === 'setup' ? (busy ? 'Creating…' : 'Create setup code') : (busy ? 'Retiring…' : 'Confirm retirement')}
              </button>
            </footer>
          </form>
        </dialog>}
      </>)}
    </section>
  )
}
