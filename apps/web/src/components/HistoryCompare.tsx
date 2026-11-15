import { useState } from 'react'
import type { History, HistoryPoint } from '../api/types'
import { label, showReading } from '../app/format'
import { segments, seriesOf } from './Chart'

const METRICS = ['water_temp_c', 'ph', 'ec_ms_cm', 'air_temp_c', 'relative_humidity_pct']
const W = 640, H = 72, L = 38, R = 8, T = 8, B = 8

function MetricRow({ history, name, selected }: { history: History; name: string; selected: HistoryPoint }) {
  const { pts, invalid } = seriesOf(history.points, name)
  const values = pts.map(p => p.v)
  let lo = values.length ? Math.min(...values) : 0
  let hi = values.length ? Math.max(...values) : 1
  const mid = (lo + hi) / 2
  const minSpan = Math.max(.5, Math.abs(mid) * .05)
  if (hi - lo < minSpan) { lo = mid - minSpan / 2; hi = mid + minSpan / 2 }
  const pad = (hi - lo) * .1; lo -= pad; hi += pad
  const t0 = Date.parse(history.from), t1 = Date.parse(history.to)
  const x = (t: number) => L + ((t - t0) / (t1 - t0 || 1)) * (W - L - R)
  const y = (v: number) => T + (1 - (v - lo) / (hi - lo)) * (H - T - B)
  const gaps = history.gaps.map(g => ({ from: Date.parse(g.from), to: Date.parse(g.to) }))
  const drawn = segments(pts, gaps)
  const number = (v: number) => new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 }).format(v)
  const selectedReading = selected.readings[name]
  const selectedValue = selectedReading?.quality === 'valid' && typeof selectedReading.value === 'number' ? selectedReading.value : null
  const selectedTime = Date.parse(selected.t)
  return <figure className="compare-row">
    <figcaption><span>{label(name)}</span><strong className={selectedValue === null ? 'dim' : ''}>{showReading(selectedReading)}</strong><small>{values.length ? `Low ${number(Math.min(...values))} · High ${number(Math.max(...values))}` : `${invalid.length} invalid or missing`}</small></figcaption>
    <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={`${label(name)} history, independently scaled from ${number(lo)} to ${number(hi)}`}>
      {[lo, hi].map((v, i) => <g key={i}><line x1={L} x2={W - R} y1={y(v)} y2={y(v)} className="grid"/><text x={L - 5} y={y(v) + 4} textAnchor="end" className="axis">{number(v)}</text></g>)}
      <line x1={L + (W - L - R) / 2} x2={L + (W - L - R) / 2} y1={T} y2={H - B} className="vgrid"/>
      {gaps.map((g, i) => <rect key={i} x={x(g.from)} y={T} width={Math.max(2, x(g.to) - x(g.from))} height={H - T - B} className="gap"><title>No data in this interval</title></rect>)}
      {drawn.map((s, i) => s.length === 1 ? <circle key={i} cx={x(s[0].t)} cy={y(s[0].v)} r="2.5" className="dot"/> : <polyline key={i} className="line" fill="none" points={s.map(p => `${x(p.t)},${y(p.v)}`).join(' ')}/>)}
      <line x1={x(selectedTime)} x2={x(selectedTime)} y1={T} y2={H - B} className="compare-cursor"/>
      {selectedValue !== null && <circle cx={x(selectedTime)} cy={y(selectedValue)} r="4" className="selected-dot"/>}
      {invalid.map((t, i) => <line key={i} x1={x(t)} x2={x(t)} y1={H - B} y2={H - B - 7} className="bad"><title>Invalid or missing reading</title></line>)}
    </svg>
  </figure>
}

export function HistoryCompare({ history }: { history: History }) {
  const [chosenTime, setChosenTime] = useState<number | null>(null)
  let selectedIndex = history.points.length - 1
  if (chosenTime !== null) for (let i = 0; i < history.points.length; i++) if (Math.abs(Date.parse(history.points[i].t) - chosenTime) < Math.abs(Date.parse(history.points[selectedIndex].t) - chosenTime)) selectedIndex = i
  const selected = history.points[selectedIndex]
  const selectedLabel = new Date(selected.t).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })
  const start = new Date(history.from).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const middle = new Date((Date.parse(history.from) + Date.parse(history.to)) / 2).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const end = new Date(history.to).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return <div className="history-compare" aria-label="Sensor history comparison">
    <div className="compare-toolbar"><div><strong>Compare one moment</strong><span>{selectedLabel}</span></div><input type="range" min={0} max={history.points.length - 1} value={selectedIndex} disabled={history.points.length < 2} onChange={e => setChosenTime(Date.parse(history.points[Number(e.target.value)].t))} aria-label="Compare readings at time" aria-valuetext={selectedLabel}/></div>
    {METRICS.map(name => <MetricRow key={name} history={history} name={name} selected={selected}/>)}
    <div className="compare-time"><svg viewBox={`0 0 ${W} 24`} role="img" aria-label={`Time axis from ${start} to ${end}`}><text x={L} y="16">{start}</text><text x={L + (W - L - R) / 2} y="16" textAnchor="middle">{middle}</text><text x={W - R} y="16" textAnchor="end">{end}</text></svg></div>
  </div>
}
