import { useEffect, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { useFetch } from '../app/hooks'
import type { Command, History, Incident, Program, Unit } from '../api/types'
import { ago, clock, label, showReading, unitStatus } from '../app/format'
import { Badge, Empty, ErrorNote, Loading, Sim } from '../components/ui'
import { HistoryCompare } from '../components/HistoryCompare'
import { ProgramEditor } from './ProgramEditor'
import { useApp } from '../app/context'
import { P } from '../api/paths'

const RANGES = [['1h', 3600e3], ['6h', 6 * 3600e3], ['24h', 24 * 3600e3], ['7d', 7 * 86400e3]] as const
const READING_ORDER = ['water_temp_c', 'ph', 'ec_ms_cm', 'reservoir_level_ok', 'air_temp_c', 'relative_humidity_pct', 'light_lux']

export function UnitPage() {
  const { unitId = '' } = useParams()
  const [searchParams] = useSearchParams()
  const tab = searchParams.get('tab') === 'history' ? 'history' : searchParams.get('tab') === 'programme' ? 'programme' : 'overview'
  useEffect(() => { window.scrollTo(0, 0) }, [tab])
  const { session } = useApp()
  const site = session!.site.id
  const [range, setRange] = useState<(typeof RANGES)[number][0]>('1h')
  const u = useFetch<{ unit: Unit; pending_command: Command | null }>(P.unit(site, unitId), ['unit.state', 'unit.reading', 'program.updated', 'incident.updated'])
  const win = RANGES.find(r => r[0] === range)![1]
  // stable "now": the history path only changes when the range changes or once a minute
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => { setNow(Date.now()); const t = window.setInterval(() => setNow(Date.now()), 60000); return () => window.clearInterval(t) }, [range])
  const hist = useFetch<History>(`${P.history(site, unitId)}?from=${encodeURIComponent(new Date(now - win).toISOString())}&to=${encodeURIComponent(new Date(now).toISOString())}`, [], 0)
  const prog = useFetch<{ applied: { version: number; program: Program; applied_at: string } | null; controller_reported_version: number | null }>(P.applied(site, unitId), ['program.updated'])
  const inc = useFetch<{ incidents: Incident[] }>(`/api/v1/sites/${site}/incidents?unit_id=${unitId}&state=active`, ['incident.updated'])

  if (u.loading) return <Loading what="unit" />
  if (!u.data) return <><ErrorNote error={u.error} retry={u.reload} /><Link to="/">← Overview</Link></>
  const unit = u.data.unit; const st = unitStatus(unit); const r = unit.latest_readings

  return (
    <div className="unit-page">
      <div className="unit-page-heading"><Link className="back-link" to="/">← Overview</Link><div className="unit-page-title"><div><h1>{unit.name.replace(/\s*\(SIMULATED\)$/i, '')}</h1>{unit.is_simulated && <Sim />}</div><Badge tone={st.tone}>{st.text}</Badge></div></div>
      <ErrorNote error={u.error} retry={u.reload} />
      <nav className="unit-tabs" aria-label="Unit sections"><Link to="?tab=overview" aria-current={tab === 'overview' ? 'page' : undefined}>Overview</Link><Link to="?tab=history" aria-current={tab === 'history' ? 'page' : undefined}>History</Link><Link to="?tab=programme" aria-current={tab === 'programme' ? 'page' : undefined}>Programme</Link></nav>
      {tab === 'overview' && <>
      <section className="panel unit-status" aria-label="Controller status">
        <h2>Controller status</h2>
        <div className="unit-facts">
          <div><span>Connection</span><strong>{unit.connection === 'ONLINE' ? 'Reporting' : unit.connection === 'NEVER_SEEN' ? 'Not seen yet' : 'Not reporting'}</strong>{unit.connection !== 'ONLINE' && unit.last_state_at && <small>Last heard {ago(unit.last_state_at)}</small>}</div>
          <div><span>Pump command</span><strong>{unit.pump_commanded ?? 'Unknown'}</strong><small>Water delivery not verified (no flow sensor)</small></div>
          <div><span>Safety interlocks</span><strong className={unit.essential_interlocks_ok === false ? 'danger-text' : ''}>{unit.essential_interlocks_ok === null ? 'Unknown' : unit.essential_interlocks_ok ? 'OK' : 'Not OK — pump held off'}</strong></div>
          <div><span>Applied programme</span><strong>{unit.applied_program_version ? `Version ${unit.applied_program_version}` : 'None'}</strong></div>
        </div>
        {unit.control_state_note && <p className="note warn">{unit.control_state_note}</p>}
        <details className="unit-technical"><summary>Controller details</summary><dl className="kv"><dt>Controller state</dt><dd>{unit.control_state ?? 'Unknown'}</dd><dt>Firmware</dt><dd>{unit.firmware_version ?? '—'}</dd><dt>Buffer</dt><dd>{unit.controller_buffer ? `${unit.controller_buffer.occupancy}/${unit.controller_buffer.capacity}` : 'Not reported'}{unit.controller_buffer && unit.controller_buffer.overflow_count > 0 && <b> · {unit.controller_buffer.overflow_count} records lost to overflow</b>}</dd></dl></details>
      </section>

      {inc.data && inc.data.incidents.length > 0 && (
        <section><h2>Active alerts for this unit</h2>
          <ul className="alerts">{inc.data.incidents.map(i => <li key={i.id} className={i.severity}><Badge tone={i.severity === 'critical' ? 'crit' : 'warn'}>{i.severity}</Badge> <b>{i.title}</b><div>{i.cause}</div><div className="muted">What to do: {i.suggested_action}</div></li>)}</ul></section>)}

      <div className="unit-section-heading"><h2>Current readings</h2>{r && <span className="muted small">Taken {ago(r.effective_at_utc)}{r.outdated && <b className="danger-text"> · OUTDATED</b>}</span>}</div>
      {!r ? <Empty>No readings have been stored for this unit yet.</Empty> : (
        <>
          <p className="muted small reading-time">{clock(r.effective_at_utc)} · {r.time_quality === 'DEVICE_CLOCK' ? 'Controller clock' : r.time_quality === 'ESTIMATED_FROM_UPTIME' ? 'Time estimated from controller uptime' : 'Time recorded when the Pi received it'}</p>
          <div className="unit-reading-grid">{Object.entries(r.readings).sort(([a], [b]) => (READING_ORDER.indexOf(a) < 0 ? 99 : READING_ORDER.indexOf(a)) - (READING_ORDER.indexOf(b) < 0 ? 99 : READING_ORDER.indexOf(b))).map(([k, v]) => <div className="unit-reading" key={k}><span>{label(k)}</span><strong className={v.quality === 'valid' ? '' : 'dim'}>{showReading(v)}</strong>{v.quality !== 'valid' && <small>{v.quality}</small>}</div>)}</div>
        </>)}
      </>}

      {tab === 'history' && <>
      <div className="history-heading"><h2>History</h2><div className="seg" role="group" aria-label="Time range">{RANGES.map(([k]) => <button key={k} aria-pressed={range === k} onClick={() => setRange(k)}>{k}</button>)}</div></div>
      <ErrorNote error={hist.error} retry={hist.reload} />
      {hist.loading && <Loading what="history" />}
      {hist.data && (hist.data.points.length === 0
        ? <Empty>No stored readings in this period.</Empty>
        : <>
            <div className="history-legend"><span>{hist.data.points.length} points · {hist.data.resolution}</span><span>Each sensor has its own scale</span><span><i className="legend-gap" /> No data</span><span><i className="legend-invalid" /> Invalid or missing</span>{hist.data.truncated && <span>Showing the first part only</span>}{hist.data.contains_simulated_data && <span>SIMULATED data</span>}</div>
            <HistoryCompare history={hist.data} />
          </>)}
      </>}

      {tab === 'programme' &&
      <section className="programme-section" aria-labelledby="programme-heading"><h2 id="programme-heading">Irrigation programme</h2>
        {prog.data?.applied
          ? <p className="programme-applied">Applied version <b>{prog.data.applied.version}</b> · confirmed {clock(prog.data.applied.applied_at)} · pump on {prog.data.applied.program.on_duration_seconds} s, off {prog.data.applied.program.off_duration_seconds} s, max continuous {prog.data.applied.program.max_continuous_on_seconds} s.</p>
          : <p className="programme-applied muted">No programme confirmed through this Pi{prog.data?.controller_reported_version ? ` · controller reports version ${prog.data.controller_reported_version}` : ''}.</p>}
        <ProgramEditor unit={unit} pending={u.data.pending_command} onChanged={() => { u.reload(); prog.reload() }} />
      </section>}
    </div>
  )
}
