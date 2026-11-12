import { useState, type FormEvent } from 'react'
import { api, ApiError } from '../api/client'
import { useApp } from '../app/context'
import { useFetch } from '../app/hooks'
import type { Role, StorageInfo } from '../api/types'
import { bytes, clock } from '../app/format'
import { ErrorNote, Loading } from '../components/ui'

interface U { id: string; username: string; role: Role; disabled: boolean }

export function Admin() {
  const { session, can } = useApp(); const site = session!.site.id
  const users = useFetch<{ users: U[] }>('/api/v1/local/users', [], 0)
  const storage = useFetch<StorageInfo>(`/api/v1/sites/${site}/storage`, [], 15000)
  const devs = useFetch<{ devices: { device_id: string; unit_id: string; data_source: string; connection: string }[] }>(`/api/v1/sites/${site}/devices`, ['unit.state'])
  const audit = useFetch<{ events: { id: string; actor_id: string; action: string; target_type: string; occurred_at: string }[] }>(`/api/v1/sites/${site}/audit?limit=20`, [], 20000)
  const [err, setErr] = useState<ApiError | null>(null)
  const [nu, setNu] = useState({ username: '', password: '', role: 'VIEWER' as Role })
  if (!can('manage_users')) return <p>Administration is available to administrators only.</p>

  const create = async (e: FormEvent) => {
    e.preventDefault(); setErr(null)
    try { await api('POST', '/api/v1/local/users', nu); setNu({ username: '', password: '', role: 'VIEWER' }); users.reload() } catch (x) { setErr(x as ApiError) }
  }
  const patch = async (id: string, body: object) => { setErr(null); try { await api('PATCH', `/api/v1/local/users/${id}`, body); users.reload() } catch (x) { setErr(x as ApiError) } }
  const s = storage.data
  return (
    <>
      <h1>Administration</h1>
      <ErrorNote error={err} />
      <h2>Storage and sync</h2>
      {storage.loading && <Loading what="storage" />}
      {s && (<>
        <p>Status: <b>{s.status === 'OK' ? 'OK' : s.status === 'LOW_SPACE' ? 'LOW SPACE' : 'WRITE FAILURES'}</b>{!s.durable_receipts_enabled && <b> – new records are NOT being saved or acknowledged</b>}</p>
        <dl className="kv"><dt>Database</dt><dd>{bytes(s.database_bytes)} (+ log {bytes(s.wal_bytes)})</dd>
          <dt>Free disk</dt><dd>{bytes(s.disk_free_bytes)} of {bytes(s.disk_total_bytes)}</dd>
          <dt>Stored snapshots</dt><dd>{s.telemetry_rows.toLocaleString()} · kept {s.retention_days} days</dd>
          <dt>Cloud sync</dt><dd>{s.cloud_sync.enabled ? `on · ${s.cloud_sync.outbox_pending} waiting` : 'off (site runs fully offline)'}</dd></dl></>)}
      <h2>Controllers</h2>
      <table className="readings"><thead><tr><th>Device</th><th>Unit</th><th>Source</th><th>Link</th></tr></thead><tbody>
        {devs.data?.devices.map(d => <tr key={d.device_id}><td>{d.device_id}</td><td>{d.unit_id}</td><td>{d.data_source === 'SIMULATOR' ? 'SIMULATED' : 'real hardware'}</td><td>{d.connection}</td></tr>)}</tbody></table>
      <h2>Local accounts</h2>
      <table className="readings"><thead><tr><th>User</th><th>Role</th><th>Status</th><th></th></tr></thead><tbody>
        {users.data?.users.map(u => (
          <tr key={u.id}><td>{u.username}</td>
            <td><select aria-label={`Role of ${u.username}`} value={u.role} onChange={e => void patch(u.id, { role: e.target.value })}>{(['ADMIN', 'OPERATOR', 'VIEWER'] as Role[]).map(r => <option key={r}>{r}</option>)}</select></td>
            <td>{u.disabled ? 'disabled' : 'active'}</td>
            <td><button onClick={() => void patch(u.id, { disabled: !u.disabled })}>{u.disabled ? 'Enable' : 'Disable'} {u.username}</button></td></tr>))}</tbody></table>
      <form onSubmit={create} className="inline-form" aria-label="Add account">
        <label>Username<input value={nu.username} onChange={e => setNu({ ...nu, username: e.target.value })} required minLength={3} autoComplete="off" /></label>
        <label>Password (12+ characters)<input type="password" value={nu.password} onChange={e => setNu({ ...nu, password: e.target.value })} required minLength={12} autoComplete="new-password" /></label>
        <label>Role<select value={nu.role} onChange={e => setNu({ ...nu, role: e.target.value as Role })}><option>VIEWER</option><option>OPERATOR</option><option>ADMIN</option></select></label>
        <button className="primary">Add account</button>
      </form>
      <h2>Recent audit events</h2>
      <ul className="plain">{audit.data?.events.map(e => <li key={e.id}><span className="muted">{clock(e.occurred_at)}</span> {e.action} {e.target_type && `· ${e.target_type}`}</li>)}</ul>
    </>
  )
}
