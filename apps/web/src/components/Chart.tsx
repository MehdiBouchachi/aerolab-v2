import type { HistoryPoint } from '../api/types'

export interface Series { t: number; v: number }

/** Split into line segments; a gap between consecutive points breaks the line. */
export function segments(pts: Series[], gaps: { from: number; to: number }[]): Series[][] {
  const out: Series[][] = []; let cur: Series[] = []
  for (let i = 0; i < pts.length; i++) {
    if (i > 0 && gaps.some(g => pts[i - 1].t >= g.from - 1 && pts[i].t <= g.to + 1)) { if (cur.length) out.push(cur); cur = [] }
    cur.push(pts[i])
  }
  if (cur.length) out.push(cur)
  return out
}

export function seriesOf(points: HistoryPoint[], key: string): { pts: Series[]; invalid: number[] } {
  const pts: Series[] = []; const invalid: number[] = []
  for (const p of points) {
    const r = p.readings[key]; const t = Date.parse(p.t)
    if (r && r.quality === 'valid' && typeof r.value === 'number') pts.push({ t, v: r.value })
    else invalid.push(t)  // invalid/missing are marked, never plotted as a number
  }
  return { pts, invalid }
}

export function Chart({ title, unit, points, gaps, from, to, keyName }: {
  title: string; unit?: string; points: HistoryPoint[]; gaps: { from: string; to: string }[]; from: string; to: string; keyName: string
}) {
  const { pts, invalid } = seriesOf(points, keyName)
  const W = 640, H = 150, L = 44, R = 8, T = 8, B = 22
  const t0 = Date.parse(from), t1 = Date.parse(to)
  if (pts.length === 0) return <figure className="chart"><figcaption>{title}</figcaption><p className="empty small">No valid readings in this period{invalid.length ? ` (${invalid.length} invalid or missing)` : ''}.</p></figure>
  let lo = Math.min(...pts.map(p => p.v)), hi = Math.max(...pts.map(p => p.v))
  // never zoom noise into drama: keep a minimum visible span (5% of the mean, at least 0.5)
  const mid = (lo + hi) / 2; const minSpan = Math.max(0.5, Math.abs(mid) * 0.05)
  if (hi - lo < minSpan) { lo = mid - minSpan / 2; hi = mid + minSpan / 2 }
  const pad = (hi - lo) * 0.1; lo -= pad; hi += pad
  const dec = hi - lo < 2 ? 2 : hi - lo < 20 ? 1 : 0
  const x = (t: number) => L + ((t - t0) / (t1 - t0 || 1)) * (W - L - R)
  const y = (v: number) => T + (1 - (v - lo) / (hi - lo)) * (H - T - B)
  const g = gaps.map(g => ({ from: Date.parse(g.from), to: Date.parse(g.to) }))
  const segs = segments(pts, g)
  return (
    <figure className="chart">
      <figcaption>{title}{unit ? ` (${unit})` : ''}</figcaption>
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={`${title} history chart`}>
        {[0, 0.5, 1].map(f => { const v = lo + (hi - lo) * f; return <g key={f}><line x1={L} x2={W - R} y1={y(v)} y2={y(v)} className="grid" /><text x={L - 4} y={y(v) + 4} textAnchor="end" className="axis">{v.toFixed(dec)}</text></g> })}
        {g.map((gp, i) => <rect key={i} x={x(gp.from)} width={Math.max(2, x(gp.to) - x(gp.from))} y={T} height={H - T - B} className="gap"><title>No data in this interval</title></rect>)}
        {segs.map((s, i) => s.length === 1
          ? <circle key={i} cx={x(s[0].t)} cy={y(s[0].v)} r={2.5} className="dot" />
          : <polyline key={i} className="line" fill="none" points={s.map(p => `${x(p.t)},${y(p.v)}`).join(' ')} />)}
        {invalid.map(t => <line key={t} x1={x(t)} x2={x(t)} y1={H - B} y2={H - B - 6} className="bad"><title>invalid or missing reading</title></line>)}
        <text x={L} y={H - 6} className="axis">{new Date(t0).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</text>
        <text x={W - R} y={H - 6} textAnchor="end" className="axis">{new Date(t1).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</text>
      </svg>
    </figure>
  )
}
