import { Tr } from '../app/language'
import { lazy, Suspense, useEffect, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useFetch } from '../app/hooks'
import type { Command, CropProfile, History, Incident, Program, Unit } from '../api/types'
import { ago, clock, incidentTitle, label, showReading, unitStatus } from '../app/format'
import { Badge, Empty, ErrorNote, Loading, Sim } from '../components/ui'
import { HistoryCompare } from '../components/HistoryCompare'
import { ProgramEditor } from './ProgramEditor'
import { useApp } from '../app/context'
import { HOME, P } from '../api/paths'

const UnitSettings = lazy(() => import('./UnitSettings').then(m => ({ default: m.UnitSettings })))

const RANGES = [['1h', 3600e3], ['6h', 6 * 3600e3], ['24h', 24 * 3600e3], ['7d', 7 * 86400e3]] as const
const READING_ORDER = ['water_temp_c', 'ph', 'ec_ms_cm', 'reservoir_level_ok', 'air_temp_c', 'relative_humidity_pct', 'light_lux']

export function UnitPage() {
  const { unitId = '' } = useParams()
  const [searchParams] = useSearchParams()
  const q = searchParams.get('tab')
  const tab = q === 'history' ? 'history' : q === 'programme' ? 'programme' : q === 'settings' ? 'settings' : 'overview'
  useEffect(() => { window.scrollTo(0, 0) }, [tab])
  const { session } = useApp()
  const site = session!.site!.id
  const [range, setRange] = useState<(typeof RANGES)[number][0]>('1h')
  const u = useFetch<{ unit: Unit; pending_command: Command | null }>(P.unit(site, unitId), ['unit.state', 'unit.reading', 'program.updated', 'incident.updated'])
  const win = RANGES.find(r => r[0] === range)![1]
  // stable "now": the history path only changes when the range changes or once a minute
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => { setNow(Date.now()); const t = window.setInterval(() => setNow(Date.now()), 60000); return () => window.clearInterval(t) }, [range])
  const hist = useFetch<History>(`${P.history(site, unitId)}?from=${encodeURIComponent(new Date(now - win).toISOString())}&to=${encodeURIComponent(new Date(now).toISOString())}`, [], 0)
  const prog = useFetch<{ applied: { version: number; program: Program; applied_at: string } | null; controller_reported_version: number | null }>(P.applied(site, unitId), ['program.updated'])
  const inc = useFetch<{ incidents: Incident[] }>(`/api/v1/sites/${site}/incidents?unit_id=${unitId}&state=active`, ['incident.updated'])
  // crop ranges live on the Pi (offline-first); the cloud copy shows only which crop is assigned
  const profileId = u.data?.unit.crop?.profile_id
  const crop = useFetch<CropProfile>(profileId ? P.site(site, `/api/v1/crop-profiles/${profileId}`) : null, ['crop_profile.updated'], 0)
  const ranges = crop.data && Object.keys(crop.data.thresholds).length ? crop.data.thresholds : null
  const nav = useNavigate()

  if (u.loading) return <Loading what="unit" />
  if (!u.data) return <><ErrorNote error={u.error} retry={u.reload} /><Link to={HOME}>← Overview</Link></>
  const unit = u.data.unit; const st = unitStatus(unit); const r = unit.latest_readings

  return (
    <div className="unit-page">
      <div className="unit-page-heading"><Link className="back-link" to={HOME}>← Overview</Link><div className="unit-page-title"><div><h1>{unit.name.replace(/\s*\(SIMULATED\)$/i, '')}</h1>{unit.is_simulated && <Sim />}</div><Badge tone={st.tone}><Tr>{st.text}</Tr></Badge></div>
        <p className="muted unit-crop">{unit.crop ? <>Growing <b>{unit.crop.crop}</b>{unit.crop.variety && ` (${unit.crop.variety})`} · profile “{unit.crop.profile_name}”</> : <Tr>{"No crop profile assigned: no crop-specific alerts."}</Tr>}</p></div>
      <ErrorNote error={u.error} retry={u.reload} />
      <nav className="unit-tabs" aria-label="Unit sections"><Link to="?tab=overview" aria-current={tab === 'overview' ? 'page' : undefined}><Tr>Overview</Tr></Link><Link to="?tab=history" aria-current={tab === 'history' ? 'page' : undefined}><Tr>History</Tr></Link><Link to="?tab=programme" aria-current={tab === 'programme' ? 'page' : undefined}><Tr>Programme</Tr></Link><Link to="?tab=settings" aria-current={tab === 'settings' ? 'page' : undefined}><Tr>Settings</Tr></Link></nav>
      {tab === 'overview' && <>
      <section className="panel unit-status" aria-label="Controller status">
        <h2><Tr>Controller status</Tr></h2>
        <div className="unit-facts">
          <div><span><Tr>Connection</Tr></span><strong>{unit.connection === 'ONLINE' ? <Tr>{"Reporting"}</Tr> : unit.connection === 'NEVER_SEEN' ? 'Not seen yet' : 'Not reporting'}</strong>{unit.connection !== 'ONLINE' && unit.last_state_at && <small>Last heard {ago(unit.last_state_at)}</small>}</div>
          <div><span><Tr>Pump command</Tr></span><strong>{unit.pump_commanded ?? 'Unknown'}</strong><small>Water delivery not verified (no flow sensor)</small></div>
          <div><span><Tr>Safety interlocks</Tr></span><strong className={unit.essential_interlocks_ok === false ? 'danger-text' : ''}>{unit.essential_interlocks_ok === null ? <Tr>{"Unknown"}</Tr> : unit.essential_interlocks_ok ? 'OK' : 'Not OK — pump held off'}</strong></div>
          <div><span><Tr>Applied programme</Tr></span><strong>{unit.applied_program_version ? `Version ${unit.applied_program_version}` : <Tr>{"None"}</Tr>}</strong></div>
        </div>
        {unit.control_state_note && <p className="note warn">{unit.control_state_note}</p>}
        <details className="unit-technical"><summary><Tr>Controller details</Tr></summary><dl className="kv"><dt>Controller state</dt><dd>{unit.control_state ?? 'Unknown'}</dd><dt>Firmware</dt><dd>{unit.firmware_version ?? '—'}</dd><dt>Buffer</dt><dd>{unit.controller_buffer ? `${unit.controller_buffer.occupancy}/${unit.controller_buffer.capacity}` : 'Not reported'}{unit.controller_buffer && unit.controller_buffer.overflow_count > 0 && <b> · {unit.controller_buffer.overflow_count} records lost to overflow</b>}</dd></dl></details>
      </section>

      {inc.data && inc.data.incidents.length > 0 && (
        <section aria-labelledby="unit-alerts-title">
          <div className="unit-section-heading"><h2 id="unit-alerts-title"><Tr>Active alerts</Tr></h2></div>
          <ul className="incident-list incident-list-surface">
            {inc.data.incidents.map(i => {
              const tone = i.severity === 'critical' ? 'crit' : i.severity === 'warning' ? 'warn' : 'muted'
              return <li key={i.id} className="incident-entry">
                <div className="incident-entry-header">
                  <div>
                    <h2>{incidentTitle(i.title)}</h2>
                    <div className="incident-entry-meta">
                      <span>Last seen {ago(i.last_seen_at)}</span>
                      {i.acknowledged_at && <span><Tr>Acknowledged</Tr></span>}
                    </div>
                  </div>
                  <Badge tone={tone}>{tone === 'crit' ? <Tr>{"Critical"}</Tr> : tone === 'warn' ? <Tr>{"Warning"}</Tr> : <Tr>{"Info"}</Tr>}</Badge>
                </div>
                <div className="incident-entry-actions">
                  <details>
                    <summary><Tr>Alert details</Tr></summary>
                    <div className="incident-entry-detail">
                      <div><strong><Tr>Suggested action</Tr></strong><p>{i.suggested_action}</p></div>
                      <div><strong><Tr>Cause</Tr></strong><p>{i.cause}</p></div>
                      {i.note && <p>{i.note}</p>}
                    </div>
                  </details>
                </div>
              </li>
            })}
          </ul>
        </section>)}

      <div className="unit-section-heading"><h2><Tr>Current readings</Tr></h2>{r && <span className="muted small">Taken {ago(r.effective_at_utc)}{r.outdated && <b className="danger-text"> · OUTDATED</b>}</span>}</div>
      {!r ? <Empty>No readings have been stored for this unit yet.</Empty> : (
        <div className="unit-readings-panel">
          <p className="muted small reading-time">{clock(r.effective_at_utc)} · {r.time_quality === 'DEVICE_CLOCK' ? 'Controller clock' : r.time_quality === 'ESTIMATED_FROM_UPTIME' ? 'Time estimated from controller uptime' : 'Time recorded when the Pi received it'}</p>
          <div className="unit-reading-grid">{Object.entries(r.readings).sort(([a], [b]) => (READING_ORDER.indexOf(a) < 0 ? 99 : READING_ORDER.indexOf(a)) - (READING_ORDER.indexOf(b) < 0 ? 99 : READING_ORDER.indexOf(b))).map(([k, v]) => {
            const rg = ranges?.[k]
            const out = !!rg && v.quality === 'valid' && typeof v.value === 'number' && ((rg.min !== undefined && v.value < rg.min) || (rg.max !== undefined && v.value > rg.max))
            return <div className={`unit-reading${out ? ' out-of-range' : ''}`} key={k}><span><Tr>{label(k)}</Tr></span><strong className={v.quality === 'valid' ? '' : 'dim'}>{showReading(v)}</strong>
              {v.quality !== 'valid' && <small>{v.quality}</small>}
              {rg && <small className="crop-range">{out && <b>outside crop range · </b>}{rg.min !== undefined && rg.max !== undefined ? `range ${rg.min}–${rg.max}` : rg.max !== undefined ? `max ${rg.max}` : `min ${rg.min}`}</small>}</div>
          })}</div>
        </div>)}
      </>}

      {tab === 'history' && <>
      <div className="history-heading"><h2><Tr>History</Tr></h2><div className="seg" role="group" aria-label="Time range">{RANGES.map(([k]) => <button key={k} aria-pressed={range === k} onClick={() => setRange(k)}>{k}</button>)}</div></div>
      <ErrorNote error={hist.error} retry={hist.reload} />
      {hist.loading && <Loading what="history" />}
      {hist.data && (hist.data.points.length === 0
        ? <Empty>No stored readings in this period.</Empty>
        : <>
            <div className="history-legend"><span>Each sensor has its own scale</span>{hist.data.gaps.length > 0 && <span><i className="legend-gap" /> <Tr>No data</Tr></span>}<span><i className="legend-invalid" /> Invalid or missing</span>{hist.data.truncated && <span>Showing the first part only</span>}{hist.data.contains_simulated_data && <span>SIMULATED data</span>}</div>
            <HistoryCompare history={hist.data} />
          </>)}
      </>}

      {tab === 'programme' &&
      <section className="programme-section" aria-labelledby="programme-heading"><h2 id="programme-heading"><Tr>Irrigation programme</Tr></h2>
        {prog.data?.applied
          ? <p className="programme-applied">Applied version <b>{prog.data.applied.version}</b> · confirmed {clock(prog.data.applied.applied_at)} · pump on {prog.data.applied.program.on_duration_seconds} s, off {prog.data.applied.program.off_duration_seconds} s, max continuous {prog.data.applied.program.max_continuous_on_seconds} s.</p>
          : <p className="programme-applied muted">No programme confirmed through this Pi{prog.data?.controller_reported_version ? ` · controller reports version ${prog.data.controller_reported_version}` : ''}.</p>}
        <ProgramEditor unit={unit} pending={u.data.pending_command} onChanged={() => { u.reload(); prog.reload() }} />
      </section>}

      {tab === 'settings' && <Suspense fallback={<Loading what="settings" />}><UnitSettings unit={unit} onChanged={u.reload} onGone={() => nav(HOME)} /></Suspense>}
    </div>
  )
}
