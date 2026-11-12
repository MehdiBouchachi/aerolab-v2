import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useFetch } from '../app/hooks'
import type { Command, History, Incident, Program, Unit } from '../api/types'
import { ago, clock, label, showReading, unitStatus } from '../app/format'
import { Badge, Empty, ErrorNote, Loading, Sim } from '../components/ui'
import { Chart } from '../components/Chart'
import { ProgramEditor } from './ProgramEditor'
import { useApp } from '../app/context'
import { P } from '../api/paths'

const RANGES = [['1h', 3600e3], ['6h', 6 * 3600e3], ['24h', 24 * 3600e3], ['7d', 7 * 86400e3]] as const
const CHARTS = ['water_temp_c', 'ph', 'ec_ms_cm', 'air_temp_c', 'relative_humidity_pct']

export function UnitPage() {
  const { unitId = '' } = useParams()
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
    <>
      <p><Link to="/">← Overview</Link></p>
      <h1>{unit.name} {unit.is_simulated && <Sim />}</h1>
      <ErrorNote error={u.error} retry={u.reload} />
      <section className="panel" aria-label="Controller status">
        <Badge tone={st.tone}>{st.text}</Badge>
        <dl className="kv">
          <dt>Connection</dt><dd>{unit.connection === 'ONLINE' ? 'Controller reporting' : unit.connection === 'NEVER_SEEN' ? 'Not heard since the Pi started' : `Not reporting – last heard ${ago(unit.last_state_at)}`}</dd>
          <dt>Controller state</dt><dd>{unit.control_state ?? 'unknown'}</dd>
          <dt>Pump</dt><dd>{unit.pump_commanded ? `Commanded ${unit.pump_commanded}` : 'unknown'} · <span className="muted">water delivery not verified (no flow sensor)</span></dd>
          <dt>Safety interlocks</dt><dd>{unit.essential_interlocks_ok === null ? 'unknown' : unit.essential_interlocks_ok ? 'OK' : <b>Not OK – pump held off</b>}</dd>
          <dt>Applied programme</dt><dd>{unit.applied_program_version ? `version ${unit.applied_program_version}` : 'none'}</dd>
          <dt>Firmware</dt><dd>{unit.firmware_version ?? '–'}{unit.controller_buffer && ` · buffer ${unit.controller_buffer.occupancy}/${unit.controller_buffer.capacity}`}{unit.controller_buffer && unit.controller_buffer.overflow_count > 0 && <b> · {unit.controller_buffer.overflow_count} records lost to overflow</b>}</dd>
        </dl>
        {unit.control_state_note && <p className="note warn">{unit.control_state_note}</p>}
      </section>

      {inc.data && inc.data.incidents.length > 0 && (
        <section><h2>Active alerts for this unit</h2>
          <ul className="alerts">{inc.data.incidents.map(i => <li key={i.id} className={i.severity}><Badge tone={i.severity === 'critical' ? 'crit' : 'warn'}>{i.severity}</Badge> <b>{i.title}</b><div>{i.cause}</div><div className="muted">What to do: {i.suggested_action}</div></li>)}</ul></section>)}

      <h2>Current readings</h2>
      {!r ? <Empty>No readings have been stored for this unit yet.</Empty> : (
        <>
          <p className="muted small">Taken {ago(r.effective_at_utc)} ({clock(r.effective_at_utc)}) · {r.time_quality === 'DEVICE_CLOCK' ? 'controller clock' : r.time_quality === 'ESTIMATED_FROM_UPTIME' ? 'time estimated from controller uptime' : 'time = when the Pi received it'}{r.outdated && <b> · OUTDATED – not current</b>}</p>
          <table className="readings"><thead><tr><th>Sensor</th><th>Value</th><th>Quality</th></tr></thead><tbody>
            {Object.entries(r.readings).map(([k, v]) => <tr key={k}><td>{label(k)}</td><td className={v.quality === 'valid' ? '' : 'dim'}>{showReading(v)}</td><td>{v.quality}</td></tr>)}
          </tbody></table>
        </>)}

      <h2>History</h2>
      <div className="seg" role="group" aria-label="Time range">{RANGES.map(([k]) => <button key={k} aria-pressed={range === k} onClick={() => setRange(k)}>{k}</button>)}</div>
      <ErrorNote error={hist.error} retry={hist.reload} />
      {hist.loading && <Loading what="history" />}
      {hist.data && (hist.data.points.length === 0
        ? <Empty>No stored readings in this period.</Empty>
        : <>
            <p className="muted small">{hist.data.points.length} points ({hist.data.resolution}). Shaded bands are periods with <b>no data</b> – nothing is drawn across them. Red ticks mark invalid or missing readings.{hist.data.truncated && ' Showing the first part only.'}{hist.data.contains_simulated_data && ' Contains SIMULATED data.'}</p>
            {CHARTS.map(k => <Chart key={k} keyName={k} title={label(k)} points={hist.data!.points} gaps={hist.data!.gaps} from={hist.data!.from} to={hist.data!.to} />)}
          </>)}

      <h2>Irrigation programme</h2>
      {prog.data?.applied
        ? <p>Applied version <b>{prog.data.applied.version}</b> (confirmed {clock(prog.data.applied.applied_at)}): pump on {prog.data.applied.program.on_duration_seconds} s, off {prog.data.applied.program.off_duration_seconds} s, max continuous {prog.data.applied.program.max_continuous_on_seconds} s.</p>
        : <p className="muted">No programme has been confirmed through this Pi yet{prog.data?.controller_reported_version ? ` (controller reports version ${prog.data.controller_reported_version})` : ''}.</p>}
      <ProgramEditor unit={unit} pending={u.data.pending_command} onChanged={() => { u.reload(); prog.reload() }} />
    </>
  )
}
