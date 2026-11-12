import { NavLink, Outlet } from 'react-router-dom'
import { useApp } from './context'
import { useFetch } from './hooks'
import type { Summary } from '../api/types'
import { ago } from './format'
import { Sim } from '../components/ui'
import { IS_CLOUD } from '../api/paths'

export function Shell() {
  const { session, stream, signOut, can, selectSite } = useApp()
  const sum = useFetch<Summary>(session ? `/api/v1/sites/${session.site.id}/summary` : null, ['unit.state', 'incident.updated', 'site.connectivity'])
  const s = sum.data
  return (
    <div className="shell">
      <header className="top">
        <div className="brand">AeroLab</div>
        <nav aria-label="Main">
          <NavLink to="/" end>Overview</NavLink>
          <NavLink to="/incidents">Alerts{s && s.active_incidents.total > 0 ? <span className="count">{s.active_incidents.total}</span> : null}</NavLink>
          {can('manage_users') && <NavLink to="/admin">Administration</NavLink>}
        </nav>
        <div className="who">
          <span className="chip" title={IS_CLOUD ? 'A synchronised copy held in the cloud. It can be behind the site.' : 'This page talks to the Raspberry Pi on this site, not the cloud'}>{IS_CLOUD ? 'CLOUD' : 'LOCAL'}</span>
          {IS_CLOUD && (session?.sites?.length ?? 0) > 1
            ? <select aria-label="Site" value={session?.site.id} onChange={e => void selectSite(e.target.value)}>{session!.sites!.map(x => <option key={x.id} value={x.id}>{x.name}</option>)}</select>
            : null}
          <span>{session?.site.name} · {session?.user.username} ({session?.user.role.toLowerCase()})</span>
          <button className="link" onClick={() => void signOut()}>Sign out</button>
        </div>
      </header>
      <div className="status-bar" role="status">
        {IS_CLOUD ? (<>
          <span>Source: <b>cloud copy</b> · refreshes every 10 s</span>
          <span>Site last synced: <b>{s ? ago(s.connectivity.last_cloud_sync_at) : '…'}</b></span>
          <span>Site data as of: <b>{s ? ago(s.observed_at) : '…'}</b></span>
          {s?.site_stale && <b className="stale-flag">SITE NOT SYNCING – showing last known values; this does not prove the pumps stopped</b>}
        </>) : (<>
          <span>Live updates: <b>{stream === 'live' ? 'connected' : stream === 'connecting' ? 'connecting…' : 'reconnecting – values may be outdated'}</b></span>
          <span>Site Wi-Fi/broker: <b>{s ? (s.connectivity.mqtt_broker_connected ? 'broker connected' : 'broker NOT connected') : '…'}</b></span>
          <span>Internet/cloud: <b>{s ? (s.connectivity.cloud_api_reachable === null ? 'not monitored (the site works without it)' : s.connectivity.cloud_api_reachable ? `reachable · last sync ${ago(s.connectivity.last_cloud_sync_at)}` : 'UNREACHABLE – syncing is queued, the site keeps working') : '…'}</b></span>
          <span>Updated: <b>{s ? ago(s.observed_at) : '…'}</b></span>
        </>)}
        {s?.contains_simulated_data && <Sim />}
      </div>
      <main><Outlet /></main>
    </div>
  )
}
