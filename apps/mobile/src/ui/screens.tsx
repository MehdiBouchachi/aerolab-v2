// Screens follow the web dashboard (apps/web/src/pages) so both clients look and read the same.
import { CameraView, useCameraPermissions } from 'expo-camera'
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { ActivityIndicator, Pressable, View } from 'react-native'
import { Text, TextInput } from './fonts'
import { FullLogo } from './Logo'
import { ApiFailure, freshnessLabel, type Reading } from '../core/session'
import { acceptableHint, newChallenge, parsePairingPayload, PairingError, verifyIdentity } from '../core/identity'
import type { Mode } from '../core/types'
import { random } from '../native'
import { useApp } from './AppState'
import { Badge, Banner, Btn, EmptyState, Loading, PageTitle, Screen, SectionHeading, Segmented, Sim } from './components'
import { ago, CARD_KEYS, label, orderReadings, SHORT_LABEL, showReading, stripSimSuffix, timeQualityText, unitStatus, type Applied, type History, type Incident, type Summary, type Unit } from './format'
import { HistoryCompare } from './HistoryCompare'
import { usePaths, useResource } from './hooks'
import { Icon } from './Icon'
import { useTheme } from './theme'

export type Go = (to: 'unit' | 'pair' | 'login' | 'alerts' | 'account', arg?: string) => void

/** Site summary is shared by the shell (status strip, alert count) and the Overview. */
export const SummaryCtx = createContext<{ r: Reading<Summary> | null; err: string | null; reload: () => Promise<void> }>({ r: null, err: null, reload: async () => {} })
export const useSummary = () => useContext(SummaryCtx)

const REASONS: Record<string, string> = {
  LOCAL_UNREACHABLE: 'The site is not reachable from here.', LOCAL_IDENTITY_MISMATCH: 'A device answered at the site address but is NOT your paired AeroLab Pi. It was ignored.',
  LOCAL_LOGIN_REQUIRED: 'Sign in to the site to see live data.', LOCAL_ONLY_MODE_NO_LOCAL: '“Local only” is on and the site is not reachable. The cloud is not used.',
  CLOUD_ONLY_MODE_NO_CLOUD: 'No cloud address is set.', CLOUD_LOGIN_REQUIRED: 'Sign in to the cloud to see data.', CLOUD_UNREACHABLE: 'The cloud is not reachable.', NO_SOURCE_AVAILABLE: 'Neither the site nor the cloud is reachable.',
}

/** Shown when data is not live: says why, how old the cached copy is, and that controls are off. */
function ConnectionNotice({ go }: { go: Go }) {
  const { c } = useTheme()
  const { decision } = useApp()
  if (!decision || decision.kind === 'live') return null
  const cloudLogin = decision.kind === 'unavailable' && decision.reason === 'CLOUD_LOGIN_REQUIRED'
  return <View style={{ gap: 8 }}>
    <Banner tone={decision.kind === 'cached' ? 'warn' : 'crit'} title={decision.kind === 'cached' ? `Cached · read only${decision.cachedFrom ? ` (${decision.cachedFrom})` : ''}` : 'Not connected'}>
      <Text style={{ fontSize: 13.5, color: c.ink }}>{REASONS[decision.reason] ?? decision.reason}{decision.kind === 'cached' && decision.cachedAt ? ` Data from ${new Date(decision.cachedAt).toLocaleString()}. Controls are disabled.` : ''}</Text>
    </Banner>
    {(decision.needsLocalLogin || cloudLogin) && <Btn kind="primary" label={cloudLogin ? 'Sign in to the cloud' : 'Sign in to the site'} onPress={() => go('login')} />}
  </View>
}

// ------------------------------------------------------------------ Overview

export function Overview({ go }: { go: Go }) {
  const { c, s } = useTheme()
  const { pairing, pairings, selectSite } = useApp()
  const p = usePaths()
  const sum = useSummary()
  const units = useResource<{ units: Unit[] }>(pairing ? p.units : null)
  const inc = useResource<{ incidents: Incident[] }>(pairing ? p.incidents('active', 5) : null)
  if (!pairing) return <Screen>
    <View style={{ paddingVertical: 12 }}><FullLogo /></View>
    <PageTitle title="Welcome" subtitle="Pair this phone with your AeroLab site to get started." />
    <EmptyState icon="qr" title="No site paired yet">On the site, an administrator opens Administration → Phone pairing to show a QR code. Join the site Wi-Fi first.</EmptyState>
    <Btn kind="primary" icon="qr" label="Pair a site" onPress={() => go('pair')} />
  </Screen>
  const d = sum.r?.data
  const list = units.r?.data.units
  return <Screen onRefresh={() => { void sum.reload(); void units.reload(); void inc.reload() }} refreshing={false}>
    <PageTitle title="Overview" subtitle={pairing.name} />
    {pairings.length > 1 && <Segmented label="Site" value={pairing.siteId} options={pairings.map(x => [x.siteId, x.name] as [string, string])} onChange={id => void selectSite(id)} />}
    <ConnectionNotice go={go} />
    {d?.site_stale && <Banner tone="warn" title="Site not syncing">Showing the last known values. This does not prove the pumps stopped.</Banner>}
    {sum.err && !sum.r && <Banner tone="warn">{sum.err}</Banner>}
    {d && <View accessibilityLabel="Site summary" style={[s.panel, { flexDirection: 'row', overflow: 'hidden' }]}>
      {([[d.units.total, 'Total units', null], [d.units.attention, 'Need attention', d.units.attention ? c.attention : null], [d.units.healthy, 'Normal', null], [d.active_incidents.critical, 'Critical alerts', d.active_incidents.critical ? c.danger : null]] as [number, string, string | null][]).map(([n, l, col], i) =>
        <View key={l} accessible accessibilityLabel={`${n} ${l}`} style={{ flex: 1, alignItems: 'center', paddingVertical: 12, paddingHorizontal: 2, borderLeftWidth: i ? 1 : 0, borderLeftColor: c.line }}>
          <Text style={{ fontSize: 22, fontWeight: '800', color: col ?? c.ink }}>{n}</Text>
          <Text numberOfLines={1} adjustsFontSizeToFit style={{ fontSize: 11, color: c.muted, marginTop: 1 }}>{l}</Text>
        </View>)}
    </View>}

    {inc.r && inc.r.data.incidents.length > 0 && <>
      <SectionHeading title="Active alerts" />
      {inc.r.data.incidents.map(i => <Pressable key={i.id} accessibilityRole="button" onPress={() => go('unit', i.unit_id)} style={({ pressed }) => [s.panel, { padding: 12, gap: 4, opacity: pressed ? 0.85 : 1 }]}>
        <View style={s.row}><Badge tone={i.severity === 'critical' ? 'crit' : 'warn'}>{i.severity}</Badge><Text style={[s.strong, { flex: 1, fontSize: 14 }]} numberOfLines={2}>{i.title}</Text></View>
        <Text style={s.muted}>{i.unit_id} · {i.suggested_action}</Text>
      </Pressable>)}
      <Btn kind="link" label="All alerts →" onPress={() => go('alerts')} />
    </>}

    <SectionHeading title="Growing units" right={list ? `${list.length} units` : undefined} />
    {!list && units.loading && <Loading what="units" />}
    {units.err && !units.r && <Banner tone="warn">{units.err}</Banner>}
    {list && list.length === 0 && <EmptyState icon="overview" title="No units yet">No units are registered yet. An administrator can register a controller under Administration on the web.</EmptyState>}
    {list?.map(u => <UnitCard key={u.id} u={u} onOpen={() => go('unit', u.id)} />)}
    {units.r?.fromCache && <Text style={s.small}>{freshnessLabel(units.r, Date.now())}</Text>}
  </Screen>
}

function UnitCard({ u, onOpen }: { u: Unit; onOpen: () => void }) {
  const { c, s } = useTheme()
  const st = unitStatus(u); const r = u.latest_readings
  return <Pressable accessibilityRole="button" accessibilityLabel={`${stripSimSuffix(u.name)}, ${st.text}. View unit`} onPress={onOpen}
    style={({ pressed }) => [s.panel, { overflow: 'hidden', backgroundColor: pressed ? c.bg : c.panel }]}>
    <View style={{ paddingHorizontal: 14, paddingTop: 13, gap: 10 }}>
      <View style={s.row}>
        <Text numberOfLines={1} style={[s.h3, { flexShrink: 1, color: st.tone === 'muted' ? c.muted : c.ink }]}>{stripSimSuffix(u.name)}</Text>
        {u.is_simulated && <Sim />}
        <View style={{ marginLeft: 'auto' }}><Badge tone={st.tone}>{st.text}</Badge></View>
      </View>
      {u.connection !== 'ONLINE' && u.last_state_at && <Text style={s.muted}>Last heard {ago(u.last_state_at)}.{u.connection === 'STALE' ? ' The controller may still be irrigating on its own programme.' : ''}</Text>}
      <View style={{ flexDirection: 'row' }}>
        {CARD_KEYS.map((k, i) => {
          const v = r?.readings[k]
          return <View key={k} accessible accessibilityLabel={`${label(k)}: ${r ? showReading(v) : 'no data'}`} style={{ flex: 1, paddingLeft: i ? 9 : 0, paddingRight: 4, borderLeftWidth: i ? 1 : 0, borderLeftColor: c.line }}>
            <Text style={{ fontSize: 11, color: c.muted }}>{SHORT_LABEL[k]}</Text>
            <Text style={{ fontSize: 14.5, fontWeight: '700', marginTop: 2, color: v?.quality === 'valid' ? c.ink : c.muted }}>{r ? showReading(v) : 'No data'}</Text>
          </View>
        })}
      </View>
      <View style={[s.divider]} />
      <View style={{ flexDirection: 'row', flexWrap: 'wrap', columnGap: 14, rowGap: 2, paddingBottom: 10 }}>
        <Text style={{ fontSize: 12, color: c.muted }}>Pump <Text style={{ color: c.ink, fontWeight: '700' }}>{u.pump_commanded ? `Commanded ${u.pump_commanded}` : 'Unknown'}</Text>  Flow not verified</Text>
        <Text style={{ fontSize: 12, color: c.muted }}>Programme <Text style={{ color: c.ink, fontWeight: '700' }}>{u.applied_program_version ? `Version ${u.applied_program_version}` : 'None applied'}</Text></Text>
      </View>
    </View>
    <View style={[s.between, { borderTopWidth: 1, borderTopColor: c.line, paddingHorizontal: 14, paddingVertical: 10 }]}>
      <Text style={[s.small, { flex: 1 }]} numberOfLines={1}>{r ? `Reading ${r.outdated ? 'outdated ' : ''}${ago(r.effective_at_utc)}${r.time_quality && r.time_quality !== 'DEVICE_CLOCK' ? ' · time estimated by the Pi' : ''}` : 'No readings stored yet'}</Text>
      <View style={s.row}><Text style={{ color: c.brand, fontWeight: '700', fontSize: 13 }}>View unit</Text><Icon name="arrow" size={15} color={c.brand} /></View>
    </View>
  </Pressable>
}

// ------------------------------------------------------------------ Unit

type UnitTab = 'overview' | 'history' | 'programme'
const RANGES = [['1h', 3600e3], ['6h', 6 * 3600e3], ['24h', 24 * 3600e3], ['7d', 7 * 86400e3]] as const

export function UnitScreen({ id, go }: { id: string; go: Go }) {
  const { c, s } = useTheme()
  const p = usePaths()
  const [tab, setTab] = useState<UnitTab>('overview')
  const unit = useResource<{ unit: Unit }>(p.unit(id))
  const u = unit.r?.data.unit
  const st = u ? unitStatus(u) : null
  return <Screen onRefresh={() => void unit.reload()} refreshing={false}>
    <View style={{ gap: 6 }}>
      <View style={[s.row, { flexWrap: 'wrap' }]}><Text accessibilityRole="header" style={[s.h1, { fontSize: 24 }]}>{u ? stripSimSuffix(u.name) : id}</Text>{u?.is_simulated && <Sim />}</View>
      {st && <View style={{ alignSelf: 'flex-start' }}><Badge tone={st.tone}>{st.text}</Badge></View>}
    </View>
    <ConnectionNotice go={go} />
    {unit.err && !unit.r && <Banner tone="warn">{unit.err}</Banner>}
    {!u && unit.loading && <Loading what="unit" />}
    <Segmented label="Unit sections" value={tab} onChange={setTab} options={[['overview', 'Overview'], ['history', 'History'], ['programme', 'Programme']]} />
    {u && tab === 'overview' && <UnitOverview u={u} />}
    {u && tab === 'history' && <UnitHistory id={id} />}
    {u && tab === 'programme' && <UnitProgramme id={id} />}
    {unit.r?.fromCache && <Text style={s.small}>{freshnessLabel(unit.r, Date.now())}</Text>}
    <View style={{ height: 4, backgroundColor: c.bg }} />
  </Screen>
}

function Fact({ k, v, sub, first, danger }: { k: string; v: string; sub?: string; first?: boolean; danger?: boolean }) {
  const { c, s } = useTheme()
  return <View style={{ flexDirection: 'row', gap: 10, paddingVertical: 10, borderTopWidth: first ? 0 : 1, borderTopColor: c.line }}>
    <Text style={[s.muted, { width: '40%' }]}>{k}</Text>
    <View style={{ flex: 1, gap: 2 }}><Text style={[s.strong, { fontSize: 14 }, danger && { color: c.danger }]}>{v}</Text>{sub ? <Text style={s.small}>{sub}</Text> : null}</View>
  </View>
}

function UnitOverview({ u }: { u: Unit }) {
  const { c, s } = useTheme()
  const [more, setMore] = useState(false)
  const r = u.latest_readings
  return <>
    <View style={[s.panel, { paddingHorizontal: 14, paddingTop: 12, paddingBottom: 4 }]}>
      <Text style={[s.h3, { fontSize: 14, marginBottom: 2 }]}>Controller status</Text>
      <Fact first k="Connection" v={u.connection === 'ONLINE' ? 'Reporting' : u.connection === 'NEVER_SEEN' ? 'Not seen yet' : 'Not reporting'} sub={u.connection !== 'ONLINE' && u.last_state_at ? `Last heard ${ago(u.last_state_at)}` : undefined} />
      <Fact k="Pump command" v={u.pump_commanded ?? 'Unknown'} sub="Water delivery not verified (no flow sensor)" />
      <Fact k="Safety interlocks" v={u.essential_interlocks_ok == null ? 'Unknown' : u.essential_interlocks_ok ? 'OK' : 'Not OK – pump held off'} danger={u.essential_interlocks_ok === false} />
      <Fact k="Applied programme" v={u.applied_program_version ? `Version ${u.applied_program_version}` : 'None'} />
      {u.control_state_note ? <View style={{ paddingBottom: 8 }}><Banner tone="warn">{u.control_state_note}</Banner></View> : null}
      <Pressable accessibilityRole="button" accessibilityState={{ expanded: more }} onPress={() => setMore(!more)} style={{ flexDirection: 'row', alignItems: 'center', gap: 6, paddingVertical: 10, borderTopWidth: 1, borderTopColor: c.line }}>
        <View style={{ transform: [{ rotate: more ? '90deg' : '0deg' }] }}><Icon name="chevron" size={14} color={c.brand} /></View>
        <Text style={{ color: c.brand, fontWeight: '600', fontSize: 14 }}>Controller details</Text>
      </Pressable>
      {more && <View style={{ paddingBottom: 10, gap: 4 }}>
        <Text style={s.muted}>Controller state <Text style={{ color: c.ink }}>{u.control_state ?? 'Unknown'}</Text></Text>
        <Text style={s.muted}>Firmware <Text style={{ color: c.ink }}>{u.firmware_version ?? '—'}</Text></Text>
      </View>}
    </View>

    <SectionHeading title="Current readings" right={r ? `Taken ${ago(r.effective_at_utc)}` : undefined} />
    {!r ? <EmptyState icon="clock" title="No readings yet">No readings have been stored for this unit yet.</EmptyState> : <>
      <Text style={s.small}>{new Date(r.effective_at_utc).toLocaleString()} · {timeQualityText(r.time_quality)}{r.outdated ? ' · OUTDATED' : ''}</Text>
      <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: 8 }}>
        {orderReadings(r.readings).map(([k, v]) => <View key={k} accessible accessibilityLabel={`${label(k)}: ${showReading(v)}`} style={[s.panel, { width: '48.5%', flexGrow: 1, paddingHorizontal: 12, paddingVertical: 10, borderRadius: 10 }]}>
          <Text style={s.muted}>{label(k)}</Text>
          <Text style={{ fontSize: 17, fontWeight: '700', marginTop: 3, color: v.quality === 'valid' ? c.ink : c.muted }}>{showReading(v)}</Text>
          {v.quality !== 'valid' && <Text style={[s.small, { textTransform: 'capitalize' }]}>{v.quality}</Text>}
        </View>)}
      </View>
    </>}
  </>
}

function UnitHistory({ id }: { id: string }) {
  const { s } = useTheme()
  const p = usePaths()
  const [range, setRange] = useState<(typeof RANGES)[number][0]>('1h')
  // a stable window per range (refreshed once a minute) so the path does not change every render
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 60000); return () => clearInterval(t) }, [])
  const win = RANGES.find(r => r[0] === range)![1]
  const hist = useResource<History>(p.history(id, new Date(now - win).toISOString(), new Date(now).toISOString()), 0)
  const h = hist.r?.data
  return <>
    <Segmented label="Time range" value={range} onChange={v => { setRange(v); setNow(Date.now()) }} options={RANGES.map(([k]) => [k, k] as [typeof k, string])} />
    {hist.err && !hist.r && <Banner tone="warn">{hist.err}</Banner>}
    {!h && hist.loading && <Loading what="history" />}
    {h && (h.points.length === 0
      ? <EmptyState icon="clock" title="No readings in this period">No stored readings in this period.</EmptyState>
      : <>
        <Text style={s.small}>{h.points.length} points · {h.resolution} · each sensor has its own scale · gaps are shaded, invalid or missing readings are marked{h.truncated ? ' · showing the first part only' : ''}{h.contains_simulated_data ? ' · SIMULATED data' : ''}</Text>
        <HistoryCompare history={h} />
      </>)}
  </>
}

function UnitProgramme({ id }: { id: string }) {
  const { s } = useTheme()
  const p = usePaths()
  const prog = useResource<Applied>(p.applied(id))
  const a = prog.r?.data
  return <>
    <SectionHeading title="Irrigation programme" />
    {!a && prog.loading && <Loading what="programme" />}
    {prog.err && !prog.r && <Banner tone="warn">{prog.err}</Banner>}
    {a && (a.applied
      ? <View style={[s.panel, { paddingHorizontal: 14, paddingTop: 4, paddingBottom: 4 }]}>
        <Fact first k="Applied version" v={String(a.applied.version)} sub={`Confirmed by the controller ${new Date(a.applied.applied_at).toLocaleString()}`} />
        <Fact k="Pump on" v={`${a.applied.program.on_duration_seconds} s`} />
        <Fact k="Pump off" v={`${a.applied.program.off_duration_seconds} s`} />
        <Fact k="Maximum continuous on" v={`${a.applied.program.max_continuous_on_seconds} s`} />
        <Fact k="Reservoir protection" v="Always on" />
      </View>
      : <EmptyState icon="clock" title="No programme applied">{`No programme confirmed through this source${a.controller_reported_version ? ` · the controller reports version ${a.controller_reported_version}` : ''}.`}</EmptyState>)}
    <Banner tone="muted" title="Changing the programme">Programme changes are made from the web dashboard, which shows the review step and the controller's confirmation.</Banner>
  </>
}

// ------------------------------------------------------------------ Alerts

export function AlertsScreen({ go }: { go: Go }) {
  const { c, s } = useTheme()
  const { decision, session } = useApp()
  const p = usePaths()
  const [state, setState] = useState<'active' | 'resolved'>('active')
  const [err, setErr] = useState<string | null>(null)
  const list = useResource<{ incidents: Incident[] }>(p.incidents(state))
  const live = decision?.kind === 'live'
  const ack = async (incident: string) => {
    setErr(null)
    try { await session.post(p.acknowledge(incident), {}, `ack-${incident}-${Array.from(random(6)).join('')}`, live); await list.reload() }
    catch (e) { setErr(e instanceof ApiFailure ? (e.status === 403 ? 'Your account cannot acknowledge alerts.' : e.message) : 'Could not acknowledge the alert.') }
  }
  const items = list.r?.data.incidents
  return <Screen onRefresh={() => void list.reload()} refreshing={false}>
    <PageTitle title="Alerts" subtitle="Controller faults and their resolution history." right={items ? <Text style={s.muted}>{items.length} {state === 'active' ? 'active' : 'resolved'}</Text> : undefined} />
    <Segmented label="Alert status" value={state} onChange={setState} options={[['active', 'Active'], ['resolved', 'History']]} />
    <ConnectionNotice go={go} />
    {(err ?? (list.err && !list.r ? list.err : null)) && <Banner tone="crit">{err ?? list.err ?? ''}</Banner>}
    {!items && list.loading && <Loading what="alerts" />}
    {items && items.length === 0 && (state === 'active'
      ? <EmptyState icon="check" title="No active alerts">New controller faults will appear here when they are reported.</EmptyState>
      : <EmptyState icon="clock" title="No resolved alerts yet">Alerts will appear here after the controller stops reporting the fault.</EmptyState>)}
    {items?.map(i => {
      const tone = i.severity === 'critical' ? 'crit' : i.severity === 'warning' ? 'warn' : 'muted'
      const col = tone === 'crit' ? c.danger : tone === 'warn' ? c.attention : c.muted
      return <View key={i.id} style={[s.panel, { overflow: 'hidden' }]}>
        <View style={{ padding: 14, gap: 10, borderLeftWidth: 3, borderLeftColor: col }}>
          <View style={[s.row, { alignItems: 'flex-start' }]}>
            <Icon name={tone === 'crit' ? 'critical' : tone === 'warn' ? 'warning' : 'alerts'} size={19} color={col} />
            <View style={{ flex: 1, gap: 2 }}>
              <Text style={{ fontSize: 11, fontWeight: '700', letterSpacing: 0.5, color: col, textTransform: 'uppercase' }}>{i.severity}{i.code ? <Text style={{ color: c.muted, fontWeight: '500' }}>  {i.code.replace(/_/g, ' ')}</Text> : null}</Text>
              <Text style={s.h3}>{i.title}</Text>
            </View>
          </View>
          <Pressable accessibilityRole="link" onPress={() => go('unit', i.unit_id)} style={[s.row, { alignSelf: 'flex-start' }]}><Text style={{ color: c.brand, fontWeight: '600' }}>{i.unit_id}</Text><Icon name="arrow" size={14} color={c.brand} /></Pressable>
          {i.cause ? <View><Text style={s.small}>CAUSE</Text><Text style={[s.text, { fontSize: 14 }]}>{i.cause}</Text></View> : null}
          <View><Text style={s.small}>SUGGESTED ACTION</Text><Text style={[s.text, { fontSize: 14 }]}>{i.suggested_action}</Text></View>
          {i.note ? <Text style={s.muted}>{i.note}</Text> : null}
          <View style={{ gap: 1 }}>
            {i.first_seen_at && <Text style={s.small}>First seen {new Date(i.first_seen_at).toLocaleString()}</Text>}
            {i.last_seen_at && <Text style={s.small}>Last seen {ago(i.last_seen_at)}</Text>}
            {i.resolved_at && <Text style={s.small}>Resolved {new Date(i.resolved_at).toLocaleString()}</Text>}
            {i.acknowledged_at && <Text style={s.small}>Acknowledged {new Date(i.acknowledged_at).toLocaleString()}</Text>}
          </View>
          {i.status.endsWith('UNACKNOWLEDGED') && <Btn label={live ? 'Acknowledge' : 'Acknowledge (connect to the site or cloud)'} disabled={!live} onPress={() => void ack(i.id)} />}
        </View>
      </View>
    })}
    <View style={[s.row, { alignItems: 'flex-start', marginTop: 4 }]}><Icon name="alerts" size={15} color={c.muted} /><Text style={[s.muted, { flex: 1 }]}>Acknowledging means you have seen an alert. The controller resolves it when the fault clears.</Text></View>
  </Screen>
}

// ------------------------------------------------------------------ Account

function Group({ title, children }: { title: string; children: ReactNode }) {
  const { s } = useTheme()
  return <View style={{ gap: 8 }}><Text style={[s.small, { fontWeight: '700', letterSpacing: 0.6, marginLeft: 2, marginTop: 6 }]}>{title.toUpperCase()}</Text>{children}</View>
}

export function AccountScreen({ go }: { go: Go }) {
  const { c, s, choice, setChoice } = useTheme()
  const { mode, setMode, pairing, pairings, selectSite, removePairing, setCloudUrl, decision, userId, signOut } = useApp()
  const [cloud, setCloud] = useState(pairing?.cloudUrl ?? '')
  const [cloudMsg, setCloudMsg] = useState<string | null>(null)
  const opts: [Mode, string, string][] = [['auto', 'Automatic', 'Use the site when reachable, otherwise the cloud.'], ['local', 'Local only', 'Only the site Wi-Fi. Never the cloud.'], ['cloud', 'Cloud only', 'Only the cloud. Never the local network.']]
  const live = decision?.kind === 'live' ? decision : null
  return <Screen>
    <PageTitle title="Account" subtitle={pairing ? pairing.name : 'No site paired'} />
    {live && <View style={[s.panel, { padding: 14, gap: 10 }]}>
      <View style={s.between}>
        <View style={{ flexShrink: 1 }}><Text style={s.strong}>{userId || 'Signed in'}</Text><Text style={s.muted}>{live.source === 'local' ? 'Connected to the site' : 'Cloud · synchronised copy, can be behind the site'}{live.insecureLab ? ' · unencrypted lab link' : ''}</Text></View>
        <View style={{ borderWidth: 1, borderColor: c.line, backgroundColor: c.bg, borderRadius: 4, paddingHorizontal: 6, paddingVertical: 2 }}><Text style={{ fontSize: 11, fontWeight: '700', color: c.muted }}>{live.source === 'local' ? 'LOCAL' : 'CLOUD'}</Text></View>
      </View>
      <Btn kind="danger" label="Sign out" onPress={() => void signOut(live.source)} />
    </View>}
    <ConnectionNotice go={go} />

    <Group title="Appearance">
      <Segmented label="Appearance" value={choice} onChange={setChoice} options={[['system', 'System'], ['light', 'Light'], ['dark', 'Dark']]} />
    </Group>

    <Group title="Connection">
      <View style={[s.panel, { overflow: 'hidden' }]}>
        {opts.map(([m, l, d], i) => <Pressable key={m} accessibilityRole="radio" accessibilityState={{ selected: mode === m }} onPress={() => void setMode(m)}
          style={({ pressed }) => ({ flexDirection: 'row', alignItems: 'center', gap: 12, padding: 14, borderTopWidth: i ? 1 : 0, borderTopColor: c.line, backgroundColor: pressed ? c.bg : c.panel })}>
          <View style={{ width: 20, height: 20, borderRadius: 10, borderWidth: 2, borderColor: mode === m ? c.brand : c.line, alignItems: 'center', justifyContent: 'center' }}>{mode === m && <View style={{ width: 10, height: 10, borderRadius: 5, backgroundColor: c.brand }} />}</View>
          <View style={{ flex: 1 }}><Text style={s.strong}>{l}</Text><Text style={s.muted}>{d}</Text></View>
        </Pressable>)}
      </View>
      <Text style={s.small}>Switching source starts a fresh connection: nothing from the other source is shown.</Text>
    </Group>

    {pairing && <Group title="Cloud address">
      <View><Text style={s.fieldLabel}>Cloud address (https)</Text>
        <TextInput style={s.input} value={cloud} onChangeText={t => { setCloud(t); setCloudMsg(null) }} autoCapitalize="none" autoCorrect={false} keyboardType="url" placeholder="https://…" placeholderTextColor={c.muted} accessibilityLabel="Cloud address" /></View>
      {cloudMsg && <Text style={{ color: c.danger, fontSize: 13 }}>{cloudMsg}</Text>}
      <Btn label="Save cloud address" onPress={() => {
        const h = acceptableHint(cloud)
        if (cloud && (!h || h.insecureLab)) { setCloudMsg('Enter an https:// address.'); return }
        void setCloudUrl(h?.url ?? ''); setCloudMsg(null)
      }} />
    </Group>}

    <Group title="Sites">
      {pairings.length > 0 && <View style={[s.panel, { overflow: 'hidden' }]}>
        {pairings.map((x, i) => <Pressable key={x.siteId} accessibilityRole="radio" accessibilityState={{ selected: x.siteId === pairing?.siteId }} onPress={() => void selectSite(x.siteId)}
          style={({ pressed }) => ({ flexDirection: 'row', alignItems: 'center', gap: 10, padding: 14, borderTopWidth: i ? 1 : 0, borderTopColor: c.line, backgroundColor: pressed ? c.bg : c.panel })}>
          <View style={{ flex: 1 }}><Text style={s.strong}>{x.name}</Text><Text style={s.small}>Paired {new Date(x.pairedAt).toLocaleDateString()}</Text></View>
          {x.siteId === pairing?.siteId && <Icon name="check" size={18} color={c.brand} />}
        </Pressable>)}
      </View>}
      <Btn icon="qr" label={pairings.length ? 'Pair another site' : 'Pair a site'} kind={pairings.length ? 'secondary' : 'primary'} onPress={() => go('pair')} />
      {pairing && <Btn kind="danger" label={`Unpair ${pairing.name}`} onPress={() => void removePairing(pairing.siteId)} />}
    </Group>
  </Screen>
}

// ------------------------------------------------------------------ Pairing and sign-in

export function PairScreen({ done }: { done: () => void }) {
  const { c, s } = useTheme()
  const { addPairing, env } = useApp()
  const [perm, ask] = useCameraPermissions()
  const [manual, setManual] = useState(false)
  const [payload, setPayload] = useState('')
  const [msg, setMsg] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const doPair = useCallback(async (raw: string) => {
    setBusy(true); setMsg(null)
    try {
      const p = parsePairingPayload(raw)
      for (const hint of p.hints) {
        const h = acceptableHint(hint); if (!h) continue
        // 1) verify the Pi BEFORE sending it the code: it must prove it holds the key printed in the QR
        const challenge = newChallenge(env.random)
        const idr = await env.fetch(`${h.url}/api/v1/identity?challenge=${encodeURIComponent(challenge)}`).then(r => r.json()).catch(() => null)
        if (!idr || !verifyIdentity({ siteId: p.site, publicKey: p.pk }, challenge, idr)) continue
        // 2) consume the single-use code; the answer must repeat the pinned key
        const r = await env.fetch(`${h.url}/api/v1/site-pairing/confirm`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: p.code, device_name: 'AeroLab phone' }) })
        if (!r.ok) { setMsg(r.status === 401 ? 'That pairing code is invalid or expired. Ask an administrator for a new one.' : 'Pairing failed. Try again with a new code.'); setBusy(false); return }
        const body = await r.json() as { site_id: string; public_key: string }
        if (body.site_id !== p.site || body.public_key !== p.pk) { setMsg('The site did not match the pairing code. Nothing was saved.'); setBusy(false); return }
        await addPairing({ siteId: p.site, name: p.name, publicKey: p.pk, localHints: p.hints, pairedAt: Date.now() })
        setBusy(false); done(); return
      }
      setMsg('Could not verify the site. Check that this phone is on the site Wi-Fi, then try again. Nothing was sent.')
    } catch (e) { setMsg(e instanceof PairingError ? e.message : 'Pairing failed. Try again with a new code.') }
    setBusy(false)
  }, [env, addPairing, done])
  return <Screen>
    <PageTitle title="Pair a site" />
    <Text style={s.muted}>On the site, an administrator opens Administration → Phone pairing to show a QR code (valid 5 minutes, single use). Join the site Wi-Fi first.</Text>
    <Segmented label="Pairing method" value={manual ? 'text' : 'camera'} onChange={v => setManual(v === 'text')} options={[['camera', 'Scan QR code'], ['text', 'Enter text']]} />
    {!manual && (perm?.granted
      ? <View style={{ height: 300, borderRadius: 12, overflow: 'hidden', borderWidth: 1, borderColor: c.line }}><CameraView style={{ flex: 1 }} barcodeScannerSettings={{ barcodeTypes: ['qr'] }} onBarcodeScanned={e => { if (!busy) void doPair(e.data) }} /></View>
      : <EmptyState icon="qr" title="Camera access needed">Allow the camera to scan the pairing QR code shown on the site's Administration page.</EmptyState>)}
    {!manual && !perm?.granted && <Btn kind="primary" label="Allow camera" onPress={() => void ask()} />}
    {manual && <>
      <View><Text style={s.fieldLabel}>Pairing text</Text>
        <TextInput style={[s.input, { minHeight: 120, paddingTop: 12, textAlignVertical: 'top' }]} multiline value={payload} onChangeText={setPayload} autoCapitalize="none" autoCorrect={false} accessibilityLabel="Pairing text" /></View>
      <Btn kind="primary" label={busy ? 'Pairing…' : 'Pair this phone'} disabled={busy || !payload} onPress={() => void doPair(payload)} />
    </>}
    {busy && <ActivityIndicator color={c.brand} />}
    {msg && <Banner tone="crit">{msg}</Banner>}
  </Screen>
}

export function LoginScreen({ done }: { done: () => void }) {
  const { c, s } = useTheme()
  const { decision, pairing, signedIn, env } = useApp()
  const wantsCloud = decision?.kind === 'unavailable' && decision.reason === 'CLOUD_LOGIN_REQUIRED'
  const [source, setSource] = useState<'local' | 'cloud'>(wantsCloud ? 'cloud' : 'local')
  const [u, setU] = useState(''); const [pw, setPw] = useState(''); const [err, setErr] = useState<string | null>(null); const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState({ u: false, p: false })
  const submit = async () => {
    setTouched({ u: true, p: true })
    if (!pairing || !u || !pw) return
    setBusy(true); setErr(null)
    try {
      let base = pairing.cloudUrl ?? ''
      if (source === 'local') {
        // find the VERIFIED local address again: credentials only ever go to the paired Pi
        const { probeLocal } = await import('../core/connection'); const pr = await probeLocal(env, pairing)
        if (!pr.ok) { setErr(pr.reason === 'LOCAL_IDENTITY_MISMATCH' ? 'That is not your paired Pi. Nothing was sent.' : 'The site is not reachable. Check that this phone is on the site Wi-Fi.'); setBusy(false); return }
        base = pr.baseUrl
      } else if (!base) { setErr('Set the cloud address under Account first.'); setBusy(false); return }
      const r = await env.fetch(base + (source === 'local' ? '/api/v1/local/auth/login' : '/api/v1/auth/login'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(source === 'local' ? { username: u, password: pw } : { email: u, password: pw }) })
      if (r.status === 401) setErr('That username and password do not match.'); else if (r.status === 429) setErr('Too many attempts. Wait a few minutes and try again.'); else if (!r.ok) setErr('Sign-in failed. Try again.')
      else { const b = await r.json() as { token: string }; await signedIn(source, u.toLowerCase(), b.token); setBusy(false); done(); return }
    } catch { setErr('Could not reach the server. Check your connection and try again.') }
    setBusy(false)
  }
  const idLabel = source === 'cloud' ? 'Email' : 'Username'
  return <Screen>
    <View style={{ paddingVertical: 12 }}><FullLogo width={220} /></View>
    <PageTitle title="Sign in" subtitle={pairing?.name} />
    <Segmented label="Account type" value={source} onChange={v => { setSource(v); setErr(null) }} options={[['local', 'Site (local)'], ['cloud', 'Cloud']]} />
    <Text style={s.muted}>{source === 'local' ? 'Local accounts work with no internet and are separate from cloud accounts.' : 'Cloud accounts are separate from site accounts.'}</Text>
    <View><Text style={s.fieldLabel}>{idLabel}</Text>
      <TextInput style={[s.input, touched.u && !u && { borderColor: c.danger }]} value={u} onChangeText={setU} onBlur={() => setTouched(t => ({ ...t, u: true }))} autoCapitalize="none" autoCorrect={false} keyboardType={source === 'cloud' ? 'email-address' : 'default'} textContentType={source === 'cloud' ? 'emailAddress' : 'username'} accessibilityLabel={idLabel} />
      {touched.u && !u && <Text style={{ color: c.danger, fontSize: 13, marginTop: 4 }}>Enter your {idLabel.toLowerCase()}.</Text>}</View>
    <View><Text style={s.fieldLabel}>Password</Text>
      <TextInput style={[s.input, touched.p && !pw && { borderColor: c.danger }]} value={pw} onChangeText={setPw} onBlur={() => setTouched(t => ({ ...t, p: true }))} secureTextEntry textContentType="password" accessibilityLabel="Password" onSubmitEditing={() => void submit()} />
      {touched.p && !pw && <Text style={{ color: c.danger, fontSize: 13, marginTop: 4 }}>Enter your password.</Text>}</View>
    {err && <Banner tone="crit">{err}</Banner>}
    <Btn kind="primary" label={busy ? 'Signing in…' : 'Sign in'} disabled={busy} onPress={() => void submit()} />
  </Screen>
}
