import { Link } from 'react-router-dom'
import { useApp } from '../app/context'
import { useFetch } from '../app/hooks'
import type { Incident, Summary, Unit } from '../api/types'
import { ago, label, showReading, unitStatus } from '../app/format'
import { Badge, Empty, ErrorNote, Loading, Sim } from '../components/ui'

const KEYS = ['water_temp_c', 'ph', 'ec_ms_cm', 'reservoir_level_ok']

export function Overview() {
  const { session } = useApp(); const site = session!.site.id
  const units = useFetch<{ units: Unit[]; total: number }>(`/api/v1/sites/${site}/units?limit=100`, ['unit.state', 'unit.reading', 'incident.updated', 'program.updated'])
  const sum = useFetch<Summary>(`/api/v1/sites/${site}/summary`, ['unit.state', 'incident.updated'])
  const inc = useFetch<{ incidents: Incident[] }>(`/api/v1/sites/${site}/incidents?state=active`, ['incident.updated'])
  const s = sum.data
  return (
    <>
      <h1>Overview</h1>
      <ErrorNote error={units.error ?? sum.error} retry={() => { units.reload(); sum.reload() }} />
      {s && (
        <section className="tiles" aria-label="Site summary">
          <div className="tile"><b>{s.units.total}</b><span>growing units</span></div>
          <div className="tile ok"><b>{s.units.healthy}</b><span>running normally</span></div>
          <div className={`tile ${s.units.attention ? 'warn' : ''}`}><b>{s.units.attention}</b><span>need attention</span></div>
          <div className={`tile ${s.units.stale ? 'warn' : ''}`}><b>{s.units.stale}</b><span>not reporting</span></div>
          <div className={`tile ${s.active_incidents.critical ? 'crit' : ''}`}><b>{s.active_incidents.critical}</b><span>critical alerts</span></div>
        </section>
      )}
      {inc.data && inc.data.incidents.length > 0 && (
        <section aria-label="Active alerts">
          <h2>Active alerts</h2>
          <ul className="alerts">
            {inc.data.incidents.slice(0, 5).map(i => (
              <li key={i.id} className={i.severity}>
                <Badge tone={i.severity === 'critical' ? 'crit' : 'warn'}>{i.severity}</Badge> <b>{i.title}</b> — <Link to={`/units/${i.unit_id}`}>{i.unit_id}</Link>
                <div className="muted">{i.suggested_action}</div>
              </li>))}
          </ul>
          <Link to="/incidents">All alerts →</Link>
        </section>
      )}
      <h2>Growing units</h2>
      {units.loading && <Loading what="units" />}
      {units.data && units.data.units.length === 0 && <Empty>No units are registered yet. An administrator can register a controller under Administration.</Empty>}
      <div className="cards">
        {units.data?.units.map(u => {
          const st = unitStatus(u); const r = u.latest_readings
          return (
            <article key={u.id} className={`card ${st.tone}`}>
              <header><h3>{u.name}</h3>{u.is_simulated && <Sim />}</header>
              <Badge tone={st.tone}>{st.text}</Badge>
              {u.connection !== 'ONLINE' && u.last_state_at && <p className="muted small">Last heard {ago(u.last_state_at)}. {u.connection === 'STALE' && 'The controller may still be irrigating on its own programme.'}</p>}
              <dl>
                <dt>Pump</dt><dd>{u.pump_commanded ? `commanded ${u.pump_commanded}` : 'unknown'}<span className="muted small"> · flow not verified</span></dd>
                <dt>Programme</dt><dd>{u.applied_program_version ? `version ${u.applied_program_version}` : 'none'}</dd>
                {KEYS.map(k => <div key={k} className="row"><dt>{label(k)}</dt><dd className={r?.readings[k]?.quality !== 'valid' ? 'dim' : ''}>{r ? showReading(r.readings[k]) : 'no data yet'}</dd></div>)}
              </dl>
              <p className="muted small">{r ? <>Reading {r.outdated ? <b>outdated</b> : ''} {ago(r.effective_at_utc)}{r.time_quality !== 'DEVICE_CLOCK' ? ' (time estimated by the Pi)' : ''}</> : 'No readings stored yet'}</p>
              <Link className="btn" to={`/units/${u.id}`}>Open unit</Link>
            </article>)
        })}
      </div>
    </>
  )
}
