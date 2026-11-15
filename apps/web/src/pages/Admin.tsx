import { useEffect, useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import { useApp } from '../app/context'
import { useFetch } from '../app/hooks'
import type { Role, StorageInfo } from '../api/types'
import { bytes, clock } from '../app/format'
import { ErrorNote, Loading } from '../components/ui'
import { Icon } from '../components/Icon'
import { IS_CLOUD } from '../api/paths'
import QRCode from 'qrcode'

interface U { id: string; username: string; role: Role; disabled: boolean }

export function Admin() {
  const { session, can } = useApp()
  const site = session!.site.id
  const users = useFetch<{ users: U[] }>('/api/v1/local/users', [], 0)
  const storage = useFetch<StorageInfo>(`/api/v1/sites/${site}/storage`, [], 15000)
  const devs = useFetch<{ devices: { device_id: string; unit_id: string; data_source: string; connection: string }[] }>(`/api/v1/sites/${site}/devices`, ['unit.state'])
  const audit = useFetch<{ events: { id: string; actor_id: string; action: string; target_type: string; occurred_at: string }[] }>(`/api/v1/sites/${site}/audit?limit=20`, [], 20000)
  const [err, setErr] = useState<ApiError | null>(null)
  const [nu, setNu] = useState({ username: '', password: '', role: 'VIEWER' as Role })
  const [userErrors, setUserErrors] = useState<{ username?: string; password?: string }>({})
  const [pair, setPair] = useState<{ code: string; expires: string; payload: string; qr: string } | null>(null)
  const [searchParams] = useSearchParams()
  const requested = searchParams.get('view')
  const view = requested === 'controllers' || requested === 'users' || requested === 'activity' || (requested === 'pairing' && !IS_CLOUD) ? requested : 'storage'
  const [userQuery, setUserQuery] = useState('')
  const [showCreate, setShowCreate] = useState(false)
  const [busyUser, setBusyUser] = useState<string | null>(null)
  useEffect(() => { window.scrollTo(0, 0) }, [view])
  if (!can('manage_users')) return <p>Administration is available to administrators only.</p>

  const startPairing = async () => {
    setErr(null)
    try {
      const r = await api<{ code: string; expires_at: string; qr_payload: string }>('POST', '/api/v1/site-pairing/start', {})
      setPair({ code: r.code, expires: r.expires_at, payload: r.qr_payload, qr: await QRCode.toDataURL(r.qr_payload, { margin: 1, width: 280 }) })
    } catch (x) { setErr(x as ApiError) }
  }
  const create = async (e: FormEvent) => {
    e.preventDefault(); setErr(null)
    const errors = {
      username: nu.username.trim().length < 3 ? 'Enter at least 3 characters.' : undefined,
      password: nu.password.length < 12 ? 'Enter at least 12 characters.' : undefined,
    }
    setUserErrors(errors)
    if (errors.username || errors.password) return
    try { await api('POST', '/api/v1/local/users', { ...nu, username: nu.username.trim() }); setNu({ username: '', password: '', role: 'VIEWER' }); setShowCreate(false); users.reload() }
    catch (x) { setErr(x as ApiError) }
  }
  const patch = async (id: string, body: object) => {
    setErr(null); setBusyUser(id)
    try { await api('PATCH', `/api/v1/local/users/${id}`, body); users.reload() }
    catch (x) { setErr(x as ApiError) }
    finally { setBusyUser(null) }
  }
  const s = storage.data
  const storageTone = s?.status === 'OK' && s.durable_receipts_enabled ? 'normal' : 'problem'
  const visibleUsers = users.data?.users.filter(u => u.username.toLowerCase().includes(userQuery.trim().toLowerCase())) ?? []

  return (
    <section className="admin-page">
      <header className="admin-heading">
        <h1>Administration</h1>
        <p>Manage site health, controllers, people, and activity.</p>
      </header>
      <nav className="admin-index" aria-label="Administration views">
        <Link to="?view=storage" aria-current={view === 'storage' ? 'page' : undefined}>Storage</Link>
        <Link to="?view=controllers" aria-current={view === 'controllers' ? 'page' : undefined}>Controllers</Link>
        <Link to="?view=users" aria-current={view === 'users' ? 'page' : undefined}>Users</Link>
        {!IS_CLOUD && <Link to="?view=pairing" aria-current={view === 'pairing' ? 'page' : undefined}>Phone pairing</Link>}
        <Link to="?view=activity" aria-current={view === 'activity' ? 'page' : undefined}>Activity</Link>
      </nav>
      <ErrorNote error={err} />

      {view === 'storage' && <section className="admin-section">
        <div className="admin-section-head"><h2>Storage and sync</h2></div>
        <ErrorNote error={storage.error} retry={storage.reload} />
        {storage.loading && <Loading what="storage" />}
        {s && <div className="admin-surface">
          <div className={`admin-health ${storageTone}`}>
            <div className="admin-health-title"><Icon name={storageTone === 'normal' ? 'check' : 'warning'} size={17} />
              <strong>{storageTone === 'normal' ? 'Storage operating normally' : s.status === 'LOW_SPACE' ? 'Disk space is low' : 'Storage needs attention'}</strong>
            </div>
            {!s.durable_receipts_enabled && <p>New records are not being saved or acknowledged.</p>}
          </div>
          <dl className="admin-facts">
            <div><dt>Database</dt><dd>{bytes(s.database_bytes)} <span>+ {bytes(s.wal_bytes)} log</span></dd></div>
            <div><dt>Free disk</dt><dd>{bytes(s.disk_free_bytes)} <span>of {bytes(s.disk_total_bytes)}</span></dd></div>
            <div><dt>Stored snapshots</dt><dd>{s.telemetry_rows.toLocaleString()} <span>retained {s.retention_days} days</span></dd></div>
            <div><dt>Cloud sync</dt><dd>{s.cloud_sync.enabled ? `On · ${s.cloud_sync.outbox_pending} waiting` : 'Off'} <span>{!s.cloud_sync.enabled && 'site runs offline'}</span></dd></div>
          </dl>
        </div>}
      </section>}

      {view === 'controllers' && <section className="admin-section">
        <div className="admin-section-head"><h2>Controllers</h2>{devs.data && <span>{devs.data.devices.length} devices</span>}</div>
        <ErrorNote error={devs.error} retry={devs.reload} />
        {devs.loading && <Loading what="controllers" />}
        {devs.data && <div className="admin-table-wrap"><table className="admin-table"><thead><tr><th>Device</th><th>Unit</th><th>Source</th><th>Connection</th></tr></thead><tbody>
          {devs.data.devices.map(d => <tr key={d.device_id}><td data-label="Device" className="admin-strong">{d.device_id}</td><td data-label="Unit">{d.unit_id}</td><td data-label="Source">{d.data_source === 'SIMULATOR' ? 'SIMULATED' : 'Real hardware'}</td><td data-label="Connection"><span className={`admin-connection ${d.connection.toLowerCase()}`}>{d.connection.replace(/_/g, ' ')}</span></td></tr>)}
        </tbody></table>{devs.data.devices.length === 0 && <p className="admin-table-empty">No controllers registered.</p>}</div>}
      </section>}

      {view === 'users' && <section className="admin-section">
        <div className="admin-section-head"><div><h2>Users</h2><p className="admin-section-description">Manage local access to this site.</p></div>{users.data && <span>{users.data.users.length} accounts</span>}</div>
        <ErrorNote error={users.error} retry={users.reload} />
        {users.loading && <Loading what="accounts" />}
        <div className="admin-user-toolbar">
          <label className="admin-user-search">Search users<input type="search" value={userQuery} onChange={e => setUserQuery(e.target.value)} placeholder="Search by username" /></label>
          <button type="button" className={showCreate ? 'secondary' : 'primary'} aria-expanded={showCreate} aria-controls="admin-create-user" onClick={() => setShowCreate(v => !v)}>{showCreate ? 'Cancel' : 'Add user'}</button>
        </div>
        {showCreate && <form id="admin-create-user" onSubmit={create} noValidate className="admin-create" aria-label="Add user">
          <div className="admin-create-heading"><h3>New user</h3><p>Create a local account for this site.</p></div>
          <div className="admin-create-fields">
            <label>Username<input value={nu.username} onChange={e => { setNu({ ...nu, username: e.target.value }); setUserErrors(v => ({ ...v, username: undefined })) }} required minLength={3} autoComplete="off" aria-invalid={!!userErrors.username} aria-describedby={userErrors.username ? 'new-user-name-error' : undefined} />{userErrors.username && <span id="new-user-name-error" className="field-error">{userErrors.username}</span>}</label>
            <label>Password <span className="admin-label-hint">12+ characters</span><input type="password" value={nu.password} onChange={e => { setNu({ ...nu, password: e.target.value }); setUserErrors(v => ({ ...v, password: undefined })) }} required minLength={12} autoComplete="new-password" aria-invalid={!!userErrors.password} aria-describedby={userErrors.password ? 'new-user-password-error' : undefined} />{userErrors.password && <span id="new-user-password-error" className="field-error">{userErrors.password}</span>}</label>
            <label>Role<select value={nu.role} onChange={e => setNu({ ...nu, role: e.target.value as Role })}><option>VIEWER</option><option>OPERATOR</option><option>ADMIN</option></select></label>
          </div>
          <div className="admin-create-actions"><button type="submit" className="primary">Create user</button></div>
        </form>}
        {users.data && <div className="admin-table-wrap"><table className="admin-table accounts-table"><thead><tr><th>User</th><th>Role</th><th>Status</th><th><span className="sr-only">Action</span></th></tr></thead><tbody>
          {visibleUsers.map(u => <tr key={u.id}>
            <td data-label="User" className="admin-strong">{u.username}</td>
            <td data-label="Role"><select aria-label={`Role of ${u.username}`} value={u.role} disabled={busyUser === u.id} onChange={e => void patch(u.id, { role: e.target.value })}>{(['ADMIN', 'OPERATOR', 'VIEWER'] as Role[]).map(r => <option key={r}>{r}</option>)}</select></td>
            <td data-label="Status"><span className={`admin-user-status ${u.disabled ? 'disabled' : ''}`}>{u.disabled ? 'Disabled' : 'Active'}</span></td>
            <td data-label="Action"><button type="button" className={u.disabled ? 'secondary' : 'danger'} disabled={busyUser === u.id} aria-busy={busyUser === u.id} onClick={() => void patch(u.id, { disabled: !u.disabled })}>{busyUser === u.id ? 'Saving…' : u.disabled ? 'Enable' : 'Disable'} {u.username}</button></td>
          </tr>)}
        </tbody></table>{visibleUsers.length === 0 && <p className="admin-table-empty">{userQuery ? 'No users match your search.' : 'No local accounts.'}</p>}</div>}
      </section>}

      {!IS_CLOUD && view === 'pairing' && <section className="admin-section">
        <div className="admin-section-head"><h2>Phone pairing</h2></div>
        <div className="admin-surface admin-pairing">
          <div><h3>Connect the AeroLab app</h3><p>Join the site Wi-Fi, then scan a one-time code in the app. The code expires after 5 minutes; sign in with a site account afterward.</p><button type="button" className="secondary" onClick={() => void startPairing()}>Show pairing code</button></div>
          {pair && <div className="admin-pair-code"><img src={pair.qr} alt={`Pairing QR code ${pair.code}`} width={280} height={280} /><p>Code <strong>{pair.code}</strong> · expires {new Date(pair.expires).toLocaleTimeString()}</p><details><summary>Manual pairing text</summary><textarea className="pairing-text resize-none" readOnly rows={4} value={pair.payload} aria-label="Pairing text" /></details></div>}
        </div>
      </section>}

      {view === 'activity' && <section className="admin-section">
        <div className="admin-section-head"><h2>Recent activity</h2><span>Latest 20 events</span></div>
        <ErrorNote error={audit.error} retry={audit.reload} />
        {audit.loading && <Loading what="activity" />}
        {audit.data && <div className="admin-surface admin-activity">{audit.data.events.length ? <ul>{audit.data.events.map(e => <li key={e.id}><time>{clock(e.occurred_at)}</time><span>{e.action.replace(/_/g, ' ').toLowerCase()}</span>{e.target_type && <small>{e.target_type.replace(/_/g, ' ').toLowerCase()}</small>}</li>)}</ul> : <p>No recent activity.</p>}</div>}
      </section>}
    </section>
  )
}
