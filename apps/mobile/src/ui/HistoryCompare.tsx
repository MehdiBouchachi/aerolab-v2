// RN port of apps/web/src/components/HistoryCompare.tsx: five aligned sensor rows, one shared time axis and
// one scrubber that selects the same recorded moment in every row. Lines never cross a missing-data gap and
// invalid/missing readings are marked, never plotted as numbers.
import { useMemo, useRef, useState } from 'react'
import { PanResponder, Text, View, type LayoutChangeEvent } from 'react-native'
import Svg, { Circle, Line, Polyline, Rect } from 'react-native-svg'
import { label, scaleOf, segments, seriesOf, showReading, type History, type HistoryPoint } from './format'
import { useTheme } from './theme'

const METRICS = ['water_temp_c', 'ph', 'ec_ms_cm', 'air_temp_c', 'relative_humidity_pct']
const H = 56, T = 4, B = 4
const fmt = (v: number) => new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 }).format(v)
const hm = (t: number) => new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

function Row({ history, name, selected, width }: { history: History; name: string; selected: HistoryPoint; width: number }) {
  const { c, s } = useTheme()
  const { pts, invalid } = seriesOf(history.points, name)
  const values = pts.map(p => p.v)
  const { lo, hi } = scaleOf(values)
  const t0 = Date.parse(history.from), t1 = Date.parse(history.to)
  const x = (t: number) => ((t - t0) / (t1 - t0 || 1)) * width
  const y = (v: number) => T + (1 - (v - lo) / (hi - lo)) * (H - T - B)
  const gaps = history.gaps.map(g => ({ from: Date.parse(g.from), to: Date.parse(g.to) }))
  const drawn = segments(pts, gaps)
  const sel = selected.readings[name]
  const selV = sel?.quality === 'valid' && typeof sel.value === 'number' ? sel.value : null
  const selT = Date.parse(selected.t)
  return <View accessible accessibilityLabel={`${label(name)} at the selected time: ${showReading(sel)}. ${values.length ? `Low ${fmt(Math.min(...values))}, high ${fmt(Math.max(...values))}` : 'No valid readings'}`}
    style={{ paddingHorizontal: 12, paddingVertical: 10, borderTopWidth: 1, borderTopColor: c.line, gap: 4 }}>
    <View style={s.between}>
      <Text style={[s.strong, { fontSize: 14 }]}>{label(name)}</Text>
      <Text style={{ fontSize: 15, fontWeight: '700', color: selV === null ? c.muted : c.ink }}>{showReading(sel)}</Text>
    </View>
    <Text style={s.small}>{values.length ? `Low ${fmt(Math.min(...values))} · High ${fmt(Math.max(...values))} · scale ${fmt(lo)}–${fmt(hi)}` : `${invalid.length} invalid or missing`}</Text>
    {width > 0 && <Svg width={width} height={H}>
      <Line x1={0} x2={width} y1={T} y2={T} stroke={c.line} strokeWidth={1} />
      <Line x1={0} x2={width} y1={H - B} y2={H - B} stroke={c.line} strokeWidth={1} />
      <Line x1={width / 2} x2={width / 2} y1={T} y2={H - B} stroke={c.line} strokeWidth={1} strokeDasharray="2,3" />
      {gaps.map((g, i) => <Rect key={i} x={x(g.from)} y={T} width={Math.max(2, x(g.to) - x(g.from))} height={H - T - B} fill={c.attentionSoft} stroke={c.attention} strokeWidth={0.5} strokeDasharray="2,2" />)}
      {drawn.map((sg, i) => sg.length === 1
        ? <Circle key={i} cx={x(sg[0].t)} cy={y(sg[0].v)} r={2.5} fill={c.brand} />
        : <Polyline key={i} fill="none" stroke={c.brand} strokeWidth={1.6} strokeLinejoin="round" points={sg.map(p => `${x(p.t)},${y(p.v)}`).join(' ')} />)}
      {invalid.map((t, i) => <Line key={i} x1={x(t)} x2={x(t)} y1={H - B} y2={H - B - 7} stroke={c.danger} strokeWidth={1.5} />)}
      <Line x1={x(selT)} x2={x(selT)} y1={T} y2={H - B} stroke={c.ink} strokeWidth={1} strokeDasharray="3,2" opacity={0.6} />
      {selV !== null && <Circle cx={x(selT)} cy={y(selV)} r={4} fill={c.panel} stroke={c.brand} strokeWidth={2} />}
    </Svg>}
  </View>
}

export function HistoryCompare({ history }: { history: History }) {
  const { c, s } = useTheme()
  const [width, setWidth] = useState(0)
  const [idx, setIdx] = useState<number | null>(null)
  const n = history.points.length
  const selectedIndex = idx === null ? n - 1 : Math.min(idx, n - 1)
  const selected = history.points[selectedIndex]
  const t0 = Date.parse(history.from), t1 = Date.parse(history.to)

  // The scrubber is the only thing that changes the selected time (touch drag, or the step buttons).
  const pick = (px: number) => {
    if (!width || n < 2) return
    const t = t0 + Math.max(0, Math.min(1, px / width)) * (t1 - t0)
    let best = 0
    for (let i = 1; i < n; i++) if (Math.abs(Date.parse(history.points[i].t) - t) < Math.abs(Date.parse(history.points[best].t) - t)) best = i
    setIdx(best)
  }
  const pickRef = useRef(pick); pickRef.current = pick
  const pan = useMemo(() => PanResponder.create({
    onStartShouldSetPanResponder: () => true,
    onMoveShouldSetPanResponder: (_e, g) => Math.abs(g.dx) > Math.abs(g.dy),
    onPanResponderTerminationRequest: () => false,
    onPanResponderGrant: e => pickRef.current(e.nativeEvent.locationX - 12),
    onPanResponderMove: e => pickRef.current(e.nativeEvent.locationX - 12),
  }), [])
  const thumbX = width ? ((Date.parse(selected.t) - t0) / (t1 - t0 || 1)) * width : 0
  const when = new Date(selected.t).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })

  return <View style={[s.panel, { overflow: 'hidden' }]} onLayout={(e: LayoutChangeEvent) => setWidth(e.nativeEvent.layout.width - 24 - 2)}>
    <View style={{ padding: 12, gap: 8 }}>
      <View style={s.between}><Text style={s.strong}>Compare one moment</Text><Text style={s.muted}>{when}</Text></View>
      <View {...pan.panHandlers} accessible accessibilityRole="adjustable" accessibilityLabel="Compare readings at time" accessibilityValue={{ text: when }}
        accessibilityActions={[{ name: 'increment' }, { name: 'decrement' }]}
        onAccessibilityAction={e => setIdx(Math.max(0, Math.min(n - 1, selectedIndex + (e.nativeEvent.actionName === 'increment' ? 1 : -1))))}
        style={{ height: 36, justifyContent: 'center', marginHorizontal: -12, paddingHorizontal: 12 }}>
        <View style={{ height: 4, borderRadius: 2, backgroundColor: c.segTrack }} />
        <View style={{ position: 'absolute', left: 12, width: Math.max(0, thumbX), height: 4, borderRadius: 2, backgroundColor: c.brand }} />
        <View style={{ position: 'absolute', left: 12 + thumbX - 11, width: 22, height: 22, borderRadius: 11, backgroundColor: c.panel, borderWidth: 2, borderColor: c.brand, shadowColor: '#000', shadowOpacity: 0.15, shadowRadius: 3, shadowOffset: { width: 0, height: 1 }, elevation: 2 }} />
      </View>
    </View>
    {METRICS.map(m => <Row key={m} history={history} name={m} selected={selected} width={width} />)}
    <View style={[s.between, { paddingHorizontal: 12, paddingVertical: 8, borderTopWidth: 1, borderTopColor: c.line }]}>
      <Text style={s.small}>{hm(t0)}</Text><Text style={s.small}>{hm((t0 + t1) / 2)}</Text><Text style={s.small}>{hm(t1)}</Text>
    </View>
  </View>
}
