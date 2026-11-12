import { NavLink, Outlet } from 'react-router-dom'
import { useApp } from './context'
import { useFetch } from './hooks'
import type { Summary } from '../api/types'
import { ago } from './format'
import { Sim } from '../components/ui'

export function Shell() {
  const { session, stream, signOut, can } = useApp()
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
          <span className="chip" title="This page talks to the Raspberry Pi on this site, not the cloud">LOCAL</span>
          <span>{session?.site.name} · {session?.user.username} ({session?.user.role.toLowerCase()})</span>
          <button className="link" onClick={() => void signOut()}>Sign out</button>
        </div>
      </header>
      <div className="status-bar" role="status">
        <span>Live updates: <b>{stream === 'live' ? 'connected' : stream === 'connecting' ? 'connecting…' : 'reconnecting – values may be outdated'}</b></span>
        <span>Site Wi-Fi/broker: <b>{s ? (s.connectivity.mqtt_broker_connected ? 'broker connected' : 'broker NOT connected') : '…'}</b></span>
        <span>Internet/cloud: <b>{s ? (s.connectivity.cloud_api_reachable === null ? 'not monitored (the site works without it)' : s.connectivity.cloud_api_reachable ? 'reachable' : 'unreachable') : '…'}</b></span>
        <span>Updated: <b>{s ? ago(s.observed_at) : '…'}</b></span>
        {s?.contains_simulated_data && <Sim />}
      </div>
      <main><Outlet /></main>
    </div>
  )
}
