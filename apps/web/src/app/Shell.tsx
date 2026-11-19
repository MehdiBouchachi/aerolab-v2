import { LanguageButton, Tr } from './language'
import { Link, NavLink, Outlet, useLocation } from 'react-router-dom'
import { useApp } from './context'
import { useFetch } from './hooks'
import type { Summary } from '../api/types'
import { ago } from './format'
import { Sim } from '../components/ui'
import { Icon } from '../components/Icon'
import { BrandLockup } from '../components/Logo'
import { HOME, IS_CLOUD } from '../api/paths'
import { useTheme } from './theme'

export function Shell() {
  const location = useLocation()
  const { theme, toggle } = useTheme()
  const { session, stream, signOut, can, selectSite } = useApp()
  const sum = useFetch<Summary>(session?.site ? `/api/v1/sites/${session.site.id}/summary` : null, ['unit.state', 'incident.updated', 'site.connectivity'])
  const s = sum.data
  return <div className="shell">
    <header className="top">
      {location.pathname.startsWith('/units/') && <Link className="top-back" to={HOME} aria-label="Back to overview"><Icon name="back" size={22} /></Link>}
      <BrandLockup />
      <details key={location.pathname + location.search} className="mobile-account" onClick={e => { if (e.target === e.currentTarget) e.currentTarget.open = false }} onKeyDown={e => { if (e.key === 'Escape') { e.currentTarget.open = false; e.currentTarget.querySelector('summary')?.focus() } }}>
        <summary aria-label="Account menu"><Icon name="admin" size={18} /><span><Tr>Account</Tr></span></summary>
        <div className="mobile-account-panel" role="group" aria-label="Account">
          <span className="sheet-grip" aria-hidden="true" />
          <span className="account-name">{session?.user.username} <span className="muted">· {session?.user.role.toLowerCase()}</span></span>
          <span className="chip">{IS_CLOUD ? <Tr>{"CLOUD"}</Tr> : <Tr>{"LOCAL"}</Tr>}</span>
          {IS_CLOUD && (session?.sites?.length ?? 0) > 1 && <select aria-label="Site" value={session?.site?.id} onChange={e => void selectSite(e.target.value)}>{session!.sites!.map(x => <option key={x.id} value={x.id}>{x.name}</option>)}</select>}
          <button className="theme-toggle" type="button" onClick={toggle}><Icon name={theme === 'dark' ? 'sun' : 'moon'} /> {theme === 'dark' ? <Tr>{"Light mode"}</Tr> : <Tr>{"Dark mode"}</Tr>}</button>
          <button className="link" type="button" onClick={() => void signOut()}><Tr>Sign out</Tr></button>
          <LanguageButton />
        </div>
      </details>
      <nav aria-label="Main">
        {IS_CLOUD && <NavLink to="/" end><Icon name="units" /><span className="nav-label-desktop"><Tr>All farms</Tr></span><span className="nav-label-mobile"><Tr>Farms</Tr></span></NavLink>}
        <NavLink to={HOME} end className={({ isActive }) => isActive || location.pathname.startsWith('/units/') ? 'active' : ''}><Icon name="overview" /><Tr>Overview</Tr></NavLink>
        <NavLink to="/incidents"><Icon name="alerts" /> <Tr>Alerts</Tr>{s && s.active_incidents.total > 0 ? <span className="count">{s.active_incidents.total}</span> : null}</NavLink>
        {<NavLink to="/crops"><Icon name="beaker" /><span className="nav-label-desktop"><Tr>Crop profiles</Tr></span><span className="nav-label-mobile"><Tr>Crops</Tr></span></NavLink>}
        {can('manage_users') && <NavLink to="/admin"><Icon name="admin" /><span className="nav-label-desktop"><Tr>Administration</Tr></span><span className="nav-label-mobile"><Tr>Admin</Tr></span></NavLink>}
      </nav>
      <div className="who">
        <LanguageButton />
        <span className="chip" title={IS_CLOUD ? 'A synchronised copy held in the cloud. It can be behind the site.' : 'This page talks to the Raspberry Pi on this site, not the cloud'}>{IS_CLOUD ? <Tr>{"CLOUD"}</Tr> : <Tr>{"LOCAL"}</Tr>}</span>
        {IS_CLOUD && (session?.sites?.length ?? 0) > 1 && <select aria-label="Site" value={session?.site?.id} onChange={e => void selectSite(e.target.value)}>{session!.sites!.map(x => <option key={x.id} value={x.id}>{x.name}</option>)}</select>}
        <span className="account-name">{session?.user.username} <span className="muted">· {session?.user.role.toLowerCase()}</span></span>
        <button className="theme-toggle" type="button" onClick={toggle} aria-label={`Switch to ${theme === 'dark' ? 'light' : 'dark'} mode`}><Icon name={theme === 'dark' ? 'sun' : 'moon'} /> {theme === 'dark' ? <Tr>{"Light mode"}</Tr> : <Tr>{"Dark mode"}</Tr>}</button>
        <button className="link" onClick={() => void signOut()}><Tr>Sign out</Tr></button>
      </div>
    </header>
    <div className={`status-bar ${IS_CLOUD ? 'status-bar-cloud' : 'status-bar-local'}`} role="status">
      {IS_CLOUD ? <>
        <span className="status-item"><Icon name="activity" /> <span className="status-label"><span className="status-label-long"><Tr>Cloud copy</Tr></span><span className="status-label-short"><Tr>Cloud</Tr></span></span> <b>refreshes every 10 s</b></span>
        <span className="status-item"><Icon name="clock" /> <span className="status-label"><span className="status-label-long"><Tr>Last synced</Tr></span><span className="status-label-short">Synced</span></span> <b>{s ? ago(s.connectivity.last_cloud_sync_at) : '…'}</b></span>
        <span className="status-item"><Tr>Site data</Tr> <b>{s ? ago(s.observed_at) : '…'}</b></span>
        {s?.site_stale && <b className="stale-flag">SITE NOT SYNCING — showing last known values; this does not prove the pumps stopped</b>}
      </> : <>
        <span className={`status-item ${stream === 'live' ? '' : 'status-warning'}`}><Icon name="activity" /> <span className="status-label"><span className="status-label-long"><Tr>Live updates</Tr></span><span className="status-label-short">Live</span></span> <b>{stream === 'live' ? <Tr>{"Connected"}</Tr> : stream === 'connecting' ? 'Connecting…' : 'Reconnecting — values may be outdated'}</b></span>
        <span className={`status-item ${s && !s.connectivity.mqtt_broker_connected ? 'status-warning' : ''}`}><Icon name="wifi" /> <span className="status-label"><span className="status-label-long"><Tr>Site broker</Tr></span><span className="status-label-short">Broker</span></span> <b>{s ? (s.connectivity.mqtt_broker_connected ? 'Connected' : 'Not connected') : '…'}</b></span>
        <span className="status-item status-cloud" title="The site keeps working without internet or cloud access"><Tr>Cloud</Tr> <b>{s ? (s.connectivity.cloud_api_reachable === null ? 'Not monitored' : s.connectivity.cloud_api_reachable ? 'Reachable' : 'Unreachable — sync queued') : '…'}</b></span>
        <span className="status-item status-updated"><Icon name="clock" /> <Tr>Updated</Tr> <b>{s ? ago(s.observed_at) : '…'}</b></span>
      </>}
      {s?.contains_simulated_data && <Sim />}
    </div>
    <main><Outlet /></main>
  </div>
}
