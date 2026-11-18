import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { useApp } from '../app/context'
import { useFetch } from '../app/hooks'
import type { Incident, Summary, Unit } from '../api/types'
import { ago, label, showReading, unitStatus } from '../app/format'
import { Badge, Empty, ErrorNote, Loading, Sim } from '../components/ui'
import { Icon } from '../components/Icon'

const KEYS = ['water_temp_c', 'ph', 'ec_ms_cm', 'reservoir_level_ok']
// Phone cards show the four readings in one row, so they need short names.
const SHORT: Record<string, string> = { water_temp_c: 'Water', ph: 'pH', ec_ms_cm: 'EC', reservoir_level_ok: 'Reservoir' }

export function Overview({ children }: { children?: ReactNode }) {
  const { session, can } = useApp(); const site = session!.site!.id
  const units = useFetch<{ units: Unit[]; total: number }>(`/api/v1/sites/${site}/units?limit=100`, ['unit.state', 'unit.reading', 'incident.updated', 'program.updated'])
  const sum = useFetch<Summary>(`/api/v1/sites/${site}/summary`, ['unit.state', 'incident.updated'])
  const inc = useFetch<{ incidents: Incident[] }>(`/api/v1/sites/${site}/incidents?state=active`, ['incident.updated'])
  const s = sum.data
  return <div className="overview-page">
    {children}
    <header className="page-heading">
      <div><h1>Overview</h1><p className="muted">{session!.site!.name}</p></div>
      {can('manage_devices') && <Link className="btn overview-add-unit" to="/units/new">Add a unit</Link>}
    </header>
    <ErrorNote error={units.error ?? sum.error} retry={() => { units.reload(); sum.reload() }} />
    {s && <section className="tiles" aria-label="Site summary">
      <div className="tile"><b>{s.units.total}</b><span>Total units</span></div>
      <div className={`tile ${s.units.attention ? 'warn' : ''}`}><b>{s.units.attention}</b><span>Need attention</span></div>
      <div className="tile"><b>{s.units.healthy}</b><span>Normal</span></div>
      <div className={`tile ${s.active_incidents.critical ? 'crit' : ''}`}><b>{s.active_incidents.critical}</b><span>Critical alerts</span></div>
    </section>}
    {inc.data && inc.data.incidents.length > 0 && <section aria-label="Active alerts"><h2>Active alerts</h2><ul className="alerts">{inc.data.incidents.slice(0, 5).map(i => <li key={i.id} className={i.severity}><Badge tone={i.severity === 'critical' ? 'crit' : 'warn'}>{i.severity}</Badge> <b>{i.title}</b> — <Link to={`/units/${i.unit_id}`}>{i.unit_id}</Link><div className="muted">{i.suggested_action}</div></li>)}</ul><Link to="/incidents">All alerts →</Link></section>}
    <div className="section-heading"><h2>Growing units</h2></div>
    {units.loading && <Loading what="units" />}
    {units.data && units.data.units.length === 0 && <Empty>No units yet.{can('manage_devices') ? ' Use “Add a unit” to connect your first controller.' : ' An administrator can add them.'}</Empty>}
    <div className="cards">{units.data?.units.map(u => {
      const st = unitStatus(u); const r = u.latest_readings
      return <article key={u.id} className={`card ${st.tone}`}>
        <header><div className="unit-title"><h3>{u.name.replace(/\s*\(SIMULATED\)$/i, '')}</h3>{u.is_simulated && <Sim />}</div><Badge tone={st.tone}>{st.text}</Badge></header>
        <p className="small card-crop">{u.crop ? <><b>{u.crop.crop}</b> · {u.crop.profile_name}</> : <span className="muted">No crop profile</span>}</p>
        {u.connection !== 'ONLINE' && u.last_state_at && <p className="muted small">Last heard {ago(u.last_state_at)}. {u.connection === 'STALE' && 'The controller may still be irrigating on its own programme.'}</p>}
        <div className="card-readings" aria-label="Latest readings">{KEYS.map(k => <div key={k} className="reading"><span><span className="label-long">{label(k)}</span><span className="label-short" aria-hidden="true">{SHORT[k]}</span></span><strong className={r?.readings[k]?.quality !== 'valid' ? 'dim' : ''}>{r ? showReading(r.readings[k]) : 'No data'}</strong></div>)}</div>
        <div className="card-meta"><div className="meta-item"><span>Pump</span><strong>{u.pump_commanded ? `Commanded ${u.pump_commanded}` : 'Unknown'}</strong><small className="muted">Flow not verified</small></div><div className="meta-item"><span>Programme</span><strong>{u.applied_program_version ? `Version ${u.applied_program_version}` : 'None applied'}</strong></div></div>
        <footer><span className="muted small">{r ? <>Reading {r.outdated ? <b>outdated</b> : ''} {ago(r.effective_at_utc)}{r.time_quality !== 'DEVICE_CLOCK' ? ' · time estimated by the Pi' : ''}</> : 'No readings stored yet'}</span><Link className="card-link" to={`/units/${u.id}`}>View unit <Icon name="arrow" size={15} /></Link></footer>
      </article>
    })}</div>
  </div>
}
