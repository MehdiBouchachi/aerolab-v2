// Screens follow the web dashboard (apps/web/src/pages) so both clients look and read the same.
import { CameraView, useCameraPermissions } from 'expo-camera'
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { ActivityIndicator, Pressable, View } from 'react-native'
import { Text, TextInput } from './fonts'
import { FullLogo } from './Logo'
import { ApiFailure, freshnessLabel, type Reading } from '../core/session'
import { acceptableCloudAddress, acceptableHint, parsePairingPayload, PairingError } from '../core/identity'
import { probeLocal } from '../core/connection'
import type { Mode } from '../core/types'
import { random } from '../native'
import { useApp } from './AppState'
import { Badge, Banner, Btn, EmptyState, Loading, PageTitle, Screen, SectionHeading, Segmented, Sim } from './components'
import { ago, CARD_KEYS, incidentTitle, label, orderReadings, SHORT_LABEL, showReading, stripSimSuffix, timeQualityText, unitStatus, type Applied, type History, type Incident, type Summary, type Unit } from './format'
import { HistoryCompare } from './HistoryCompare'
import { usePaths, useResource, useSiteCapabilities } from './hooks'
import { AddUnitModal } from './SiteTools'
import { Icon } from './Icon'
import { useTheme } from './theme'
import { useLanguage } from './language'

export type Go = (to: 'unit' | 'pair' | 'login' | 'alerts' | 'account' | 'admin', arg?: string) => void

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
  const { pairing, pairings, selectSite, decision } = useApp()
  const p = usePaths()
  const sum = useSummary()
  const units = useResource<{ units: Unit[] }>(pairing ? p.units : null)
  const permissions = useSiteCapabilities()
  const [adding, setAdding] = useState(false)
  const canAdd = decision?.kind === 'live' && !!permissions.r?.data.capabilities.includes('manage_devices')
  if (!pairing) return <Screen>
    <View style={{ paddingVertical: 12 }}><FullLogo /></View>
    <PageTitle title="Welcome" />
    <EmptyState icon="qr" title="No site paired yet">On the site, an administrator opens Administration → Phone pairing to show a QR code. Join the site Wi-Fi first.</EmptyState>
    <Btn kind="primary" icon="qr" label="Pair a site" onPress={() => go('pair')} />
  </Screen>
  const d = sum.r?.data
  const list = units.r?.data.units
  return <Screen onRefresh={() => { void sum.reload(); void units.reload() }} refreshing={false}>
    <View style={s.between}>
      <View style={{ gap: 2, flex: 1 }}>
        <Text accessibilityRole="header" style={[s.h1, { fontSize: 19, lineHeight: 24 }]}>Overview</Text>
        <Text style={s.small}>{pairing.name}</Text>
      </View>
      {canAdd && <Btn kind="primary" label="Add a unit" onPress={() => setAdding(true)} />}
    </View>
    {adding && <AddUnitModal key={pairing.siteId} onClose={() => setAdding(false)}
      onCreated={() => { void units.reload(); void sum.reload() }} onOpen={id => { setAdding(false); go('unit', id) }} />}
    {pairings.length > 1 && <Segmented label="Site" value={pairing.siteId} options={pairings.map(x => [x.siteId, x.name] as [string, string])} onChange={id => void selectSite(id)} />}
    <ConnectionNotice go={go} />
    {d?.site_stale && <Banner tone="warn" title="Site not syncing">Showing the last known values. This does not prove the pumps stopped.</Banner>}
    {sum.err && !sum.r && <Banner tone="warn">{sum.err}</Banner>}
    {d && <View accessibilityLabel="Site summary" style={[s.panel, { flexDirection: 'row', overflow: 'hidden' }]}>
      {([[d.units.total, 'Total units', null], [d.units.attention, 'Need attention', d.units.attention ? c.attention : null], [d.units.healthy, 'Normal', null], [d.active_incidents.critical, 'Critical alerts', d.active_incidents.critical ? c.danger : null]] as [number, string, string | null][]).map(([n, l, col], i) =>
        <View key={l} accessible accessibilityLabel={`${n} ${l}`} style={{ flex: 1, alignItems: 'center', paddingVertical: 8, paddingHorizontal: 3, borderLeftWidth: i ? 1 : 0, borderLeftColor: c.line }}>
          <Text style={{ fontSize: 17, fontWeight: '700', color: col ?? c.ink }}>{n}</Text>
          <Text style={{ fontSize: 10.5, color: c.muted, textAlign: 'center' }}>{l}</Text>
        </View>)}
    </View>}

    {!!d && (d.active_incidents.total ?? d.active_incidents.critical + d.active_incidents.warning) > 0 &&
      <Btn kind="link" label="View active alerts" onPress={() => go('alerts')} />}

    <SectionHeading title="Growing units" />
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
    <View style={{ padding: 10, gap: 7 }}>
      <View style={[s.between, { alignItems: 'flex-start' }]}>
        <View style={{ flex: 1, gap: 3 }}>
          <Text style={[s.strong, { fontSize: 13 }]}>{stripSimSuffix(u.name)}</Text>
          <View style={[s.row, { flexWrap: 'wrap', gap: 4 }]}>
            {u.is_simulated && <Sim />}
            <Badge tone={st.tone}>{st.text}</Badge>
          </View>
        </View>
        <Icon name="arrow" size={16} color={c.muted} />
      </View>
      {u.connection !== 'ONLINE' && u.last_state_at && <Text style={s.small}>Last heard {ago(u.last_state_at)}</Text>}
      <View style={{ flexDirection: 'row' }}>
        {CARD_KEYS.map((k, i) => {
          const v = r?.readings[k]
          return <View key={k} accessible accessibilityLabel={`${label(k)}: ${r ? showReading(v) : 'no data'}`} style={{ flex: 1, paddingLeft: i ? 9 : 0, paddingRight: 4, borderLeftWidth: i ? 1 : 0, borderLeftColor: c.line }}>
            <Text style={{ fontSize: 11, color: c.muted }}>{SHORT_LABEL[k]}</Text>
            <Text style={{ fontSize: 12, fontWeight: '600', marginTop: 2, color: v?.quality === 'valid' ? c.ink : c.muted }}>{r ? showReading(v) : 'No data'}</Text>
          </View>
        })}
      </View>
      {r && <Text style={s.small}>{r.outdated ? 'Outdated reading' : `Updated ${ago(r.effective_at_utc)}`}</Text>}
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
      <View style={[s.row, { flexWrap: 'wrap' }]}><Text accessibilityRole="header" style={s.h1}>{u ? stripSimSuffix(u.name) : id}</Text>{u?.is_simulated && <Sim />}</View>
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
  const p = usePaths()
  const alerts = useResource<{ incidents: Incident[] }>(p.incidents('active', 100, u.id))
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

    {!!alerts.r?.data.incidents.length && <>
      <SectionHeading title="Active alerts" />
      <AlertRows items={alerts.r.data.incidents} />
    </>}
    <SectionHeading title="Current readings" right={r ? `Taken ${ago(r.effective_at_utc)}` : undefined} />
    {!r ? <EmptyState icon="clock" title="No readings yet">No readings have been stored for this unit yet.</EmptyState> : <View style={s.panel}>
      <Text style={[s.small, { padding: 10, borderBottomWidth: 1, borderBottomColor: c.line }]}>{new Date(r.effective_at_utc).toLocaleString()} · {timeQualityText(r.time_quality)}{r.outdated ? ' · OUTDATED' : ''}</Text>
      <View style={{ flexDirection: 'row', flexWrap: 'wrap', padding: 4 }}>
        {orderReadings(r.readings).map(([k, v]) => <View key={k} accessible accessibilityLabel={`${label(k)}: ${showReading(v)}${v.quality !== 'valid' ? `, ${v.quality}` : ''}`} style={{ width: '50%', paddingHorizontal: 10, paddingVertical: 9 }}>
          <Text style={s.muted}>{label(k)}</Text>
          <Text style={{ fontSize: 17, fontWeight: '700', marginTop: 3, color: v.quality === 'valid' ? c.ink : c.muted }}>{showReading(v)}</Text>
          {v.quality !== 'valid' && <Text style={[s.small, { textTransform: 'capitalize' }]}>{v.quality}</Text>}
        </View>)}
      </View>
    </View>}
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
    <Text style={s.small}>Edit timing in the web dashboard.</Text>
  </>
}

// ------------------------------------------------------------------ Alerts

export function AlertsScreen({ go }: { go: Go }) {
  const { s } = useTheme()
  const { decision, session } = useApp()
  const p = usePaths()
  const [state, setState] = useState<'active' | 'resolved'>('active')
  const [err, setErr] = useState<string | null>(null)
  const list = useResource<{ incidents: Incident[] }>(p.incidents(state))
  const permissions = useSiteCapabilities()
  const [acknowledging, setAcknowledging] = useState<string | null>(null)
  const live = decision?.kind === 'live'
  const canAck = live && !!permissions.r?.data.capabilities.includes('ack_incident')
  const ack = async (incident: string) => {
    if (!canAck || acknowledging) return
    setAcknowledging(incident)
    setErr(null)
    try { await session.post(p.acknowledge(incident), {}, `ack-${incident}-${Array.from(random(6)).join('')}`, live); await list.reload() }
    catch (e) { setErr(e instanceof ApiFailure ? (e.status === 403 ? 'Your account cannot acknowledge alerts.' : e.message) : 'Could not acknowledge the alert.') }
    finally { setAcknowledging(null) }
  }
  const items = list.r?.data.incidents
  return <Screen onRefresh={() => void list.reload()} refreshing={false}>
    <PageTitle title="Alerts" right={items?.length ? <Text style={s.muted}>{items.length} {state === 'active' ? 'active' : 'resolved'}</Text> : undefined} />
    <Segmented label="Alert status" value={state} onChange={setState} options={[['active', 'Active'], ['resolved', 'History']]} />
    <ConnectionNotice go={go} />
    {(err ?? (list.err && !list.r ? list.err : null)) && <Banner tone="crit">{err ?? list.err ?? ''}</Banner>}
    {!items && list.loading && <Loading what="alerts" />}
    {items && items.length === 0 && (state === 'active'
      ? <EmptyState icon="check" title="No active alerts">Reported faults appear here.</EmptyState>
      : <EmptyState icon="clock" title="No resolved alerts yet">Cleared faults appear here.</EmptyState>)}
    {!!items?.length && <AlertRows items={items} onUnit={id => go('unit', id)}
      onAck={canAck ? id => void ack(id) : undefined} acknowledging={acknowledging} />}
    {state === 'active' && canAck && items?.some(i => i.status.endsWith('UNACKNOWLEDGED')) &&
      <Text style={s.small}>Acknowledging marks an alert as seen. The controller clears the fault.</Text>}
  </Screen>
}

function AlertRows({ items, onUnit, onAck, acknowledging }: {
  items: Incident[]; onUnit?: (id: string) => void; onAck?: (id: string) => void; acknowledging?: string | null
}) {
  const { c, s } = useTheme()
  const [expanded, setExpanded] = useState<string | null>(null)
  return <View style={s.panel}>
    {items.map((i, index) => {
      const open = expanded === i.id
      const tone = i.severity === 'critical' ? 'crit' : i.severity === 'warning' ? 'warn' : 'muted'
      return <View key={i.id} style={{ padding: 12, gap: 5, borderTopWidth: index ? 1 : 0, borderTopColor: c.line }}>
        <View style={[s.between, { alignItems: 'flex-start' }]}>
          <Text style={[s.strong, { flex: 1 }]}>{incidentTitle(i.title)}</Text>
          <Badge tone={tone}>{tone === 'crit' ? 'Critical' : tone === 'warn' ? 'Warning' : 'Info'}</Badge>
        </View>
        <Text style={s.small}>{i.resolved_at ? `Resolved ${ago(i.resolved_at)}` : `Last seen ${ago(i.last_seen_at)}`}{i.acknowledged_at ? ' / Acknowledged' : ''}</Text>
        <View style={[s.row, { flexWrap: 'wrap', columnGap: 16 }]}>
          <Pressable accessibilityRole="button" accessibilityState={{ expanded: open }} onPress={() => setExpanded(open ? null : i.id)}
            style={[s.row, { minHeight: 44 }]}>
            <Icon name="chevron" size={13} color={c.muted} />
            <Text style={s.muted}>{open ? 'Hide details' : 'Alert details'}</Text>
          </Pressable>
          {onUnit && <Btn kind="link" label="View unit" onPress={() => onUnit(i.unit_id)} />}
        </View>
        {open && <View style={{ gap: 6 }}>
          <Text style={s.strong}>Suggested action</Text><Text style={s.text}>{i.suggested_action}</Text>
          {i.cause && <><Text style={s.strong}>Cause</Text><Text style={s.text}>{i.cause}</Text></>}
          {i.note && <Text style={s.muted}>{i.note}</Text>}
          {i.first_seen_at && <Text style={s.small}>First seen {new Date(i.first_seen_at).toLocaleString()}</Text>}
          {i.acknowledged_at && <Text style={s.small}>Acknowledged {new Date(i.acknowledged_at).toLocaleString()}</Text>}
          {i.code && <Text style={s.small}>{i.code}</Text>}
        </View>}
        {onAck && i.status.endsWith('UNACKNOWLEDGED') && <Btn label={acknowledging === i.id ? 'Acknowledging...' : 'Acknowledge'}
          disabled={!!acknowledging} onPress={() => onAck(i.id)} style={{ alignSelf: 'flex-start' }} />}
      </View>
    })}
  </View>
}

// ------------------------------------------------------------------ Account

function Group({ title, children }: { title: string; children: ReactNode }) {
  const { s } = useTheme()
  return <View style={{ gap: 8 }}><Text style={[s.small, { fontWeight: '700', marginStart: 2, marginTop: 6 }]}>{title}</Text>{children}</View>
}

export function AccountScreen({ go }: { go: Go }) {
  const { language, toggle } = useLanguage()
  const { c, s, choice, setChoice } = useTheme()
  const { mode, setMode, pairing, pairings, selectSite, removePairing, setCloudUrl, decision, userId, signOut } = useApp()
  const [cloud, setCloud] = useState(pairing?.cloudUrl ?? '')
  const [cloudMsg, setCloudMsg] = useState<string | null>(null)
  useEffect(() => { setCloud(pairing?.cloudUrl ?? ''); setCloudMsg(null) }, [pairing?.siteId, pairing?.cloudUrl])
  const opts: [Mode, string, string?][] = [['auto', 'Automatic', 'Site first, then cloud'], ['local', 'Local only'], ['cloud', 'Cloud only']]
  const live = decision?.kind === 'live' ? decision : null
  const permissions = useSiteCapabilities()
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
    {live && permissions.r?.data.capabilities.includes('manage_users') &&
      <Btn icon="admin" label="Administration" onPress={() => go('admin')} />}

    <Group title="Appearance">
      <Segmented label="Appearance" value={choice} onChange={setChoice} options={[['system', 'System'], ['light', language === 'ar' ? 'فاتح' : 'Light'], ['dark', 'Dark']]} />
    </Group>
    <Group title="Language">
      <Btn label={language === 'en' ? 'العربية' : 'English'} onPress={toggle} />
    </Group>

    <Group title="Connection">
      <View style={[s.panel, { overflow: 'hidden' }]}>
        {opts.map(([m, l, d], i) => <Pressable key={m} accessibilityRole="radio" accessibilityState={{ selected: mode === m }} onPress={() => void setMode(m)}
          style={({ pressed }) => ({ flexDirection: 'row', alignItems: 'center', gap: 12, padding: 14, borderTopWidth: i ? 1 : 0, borderTopColor: c.line, backgroundColor: pressed ? c.bg : c.panel })}>
          <View style={{ width: 20, height: 20, borderRadius: 10, borderWidth: 2, borderColor: mode === m ? c.brand : c.line, alignItems: 'center', justifyContent: 'center' }}>{mode === m && <View style={{ width: 10, height: 10, borderRadius: 5, backgroundColor: c.brand }} />}</View>
          <View style={{ flex: 1 }}><Text style={s.strong}>{l}</Text>{d && <Text style={s.muted}>{d}</Text>}</View>
        </Pressable>)}
      </View>
    </Group>

    {pairing && <Group title="Cloud address">
      <View><Text style={s.fieldLabel}>Cloud address</Text>
        <TextInput style={s.input} value={cloud} onChangeText={t => { setCloud(t); setCloudMsg(null) }} autoCapitalize="none" autoCorrect={false} keyboardType="url" placeholder="https://…" placeholderTextColor={c.muted} accessibilityLabel="Cloud address" /></View>
      {cloudMsg && <Text style={{ color: c.danger, fontSize: 13 }}>{cloudMsg}</Text>}
      <Btn label="Save cloud address" onPress={() => {
        const value = cloud.trim()
        const h = value ? acceptableCloudAddress(value) : null
        if (value && !h) { setCloudMsg('Use HTTPS, or a private Wi-Fi address for the Docker demo.'); return }
        void setCloudUrl(h?.url ?? '').catch(() => setCloudMsg('Could not save the cloud address.'))
      }} />
      {pairing.cloudUrl && <Btn kind="link" label="Sign in to cloud" onPress={() => go('login', 'cloud')} />}
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
  const [siteAddress, setSiteAddress] = useState('')
  const [msg, setMsg] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const doPair = useCallback(async (raw: string) => {
    setBusy(true); setMsg(null)
    try {
      const p = parsePairingPayload(raw)
      const suppliedAddress = siteAddress.trim()
      const override = suppliedAddress ? acceptableHint(suppliedAddress) : null
      if (suppliedAddress && !override) {
        setMsg('Enter the site address including http:// or https://.')
        return
      }
      const localHints = (override ? [override.url] : p.hints).filter(hint => !/^https?:\/\/(localhost|127\.[\d.]+)(?::\d+)?$/i.test(hint))
      if (!localHints.length) {
        setMsg('This QR points to localhost. Enter the computer’s Wi-Fi address below, then scan the QR again.')
        return
      }
      const pairing = { siteId: p.site, name: p.name, publicKey: p.pk, localHints, pairedAt: Date.now() }
      const probe = await probeLocal(env, pairing)
      if (probe.ok) {
        // 2) consume the single-use code; the answer must repeat the pinned key
        const r = await env.fetch(`${probe.baseUrl}/api/v1/site-pairing/confirm`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: p.code, device_name: 'AeroLab phone' }) })
        if (!r.ok) { setMsg(r.status === 401 ? 'That pairing code is invalid or expired. Ask an administrator for a new one.' : 'Pairing failed. Try again with a new code.'); setBusy(false); return }
        const body = await r.json() as { site_id: string; public_key: string }
        if (body.site_id !== p.site || body.public_key !== p.pk) { setMsg('The site did not match the pairing code. Nothing was saved.'); setBusy(false); return }
        await addPairing(pairing)
        setBusy(false); done(); return
      }
      setMsg(probe.reason === 'LOCAL_IDENTITY_MISMATCH'
        ? 'The site responded, but its identity did not match the QR. Generate a fresh QR from that site.'
        : `Could not reach the site at ${localHints[0]}. Open that address in Chrome to check the connection.`)
    } catch (e) { setMsg(e instanceof PairingError ? e.message : 'Pairing failed. Try again with a new code.') }
    finally { setBusy(false) }
  }, [env, addPairing, done, siteAddress])
  return <Screen>
    <PageTitle title="Pair a site" />
    <Text style={s.muted}>Scan a QR from Administration → Phone pairing. Codes expire after five minutes.</Text>
    <Segmented label="Pairing method" value={manual ? 'text' : 'camera'} onChange={v => setManual(v === 'text')} options={[['camera', 'Scan QR code'], ['text', 'Enter text']]} />
    <View>
      <Text style={s.fieldLabel}>Site address (optional)</Text>
      <TextInput style={s.input} value={siteAddress} onChangeText={setSiteAddress} placeholder="http://site-address:8080"
        placeholderTextColor={c.muted} autoCapitalize="none" autoCorrect={false} keyboardType="url" accessibilityLabel="Site address" />
    </View>
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

export function LoginScreen({ done, cloud }: { done: () => void; cloud?: boolean }) {
  const { c, s } = useTheme()
  const { decision, pairing, signedIn, env } = useApp()
  const wantsCloud = cloud || (decision?.kind === 'unavailable' && decision.reason === 'CLOUD_LOGIN_REQUIRED')
  const [source, setSource] = useState<'local' | 'cloud'>(wantsCloud ? 'cloud' : 'local')
  const [u, setU] = useState(''); const [pw, setPw] = useState(''); const [err, setErr] = useState<string | null>(null); const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState({ u: false, p: false })
  const submit = async () => {
    setTouched({ u: true, p: true })
    if (!pairing || !u || !pw) return
    setBusy(true); setErr(null)
    try {
      let base = acceptableCloudAddress(pairing.cloudUrl ?? '')?.url ?? ''
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
    {source === 'cloud' && acceptableCloudAddress(pairing?.cloudUrl ?? '')?.insecureLab &&
      <Banner tone="warn">This demo connection is unencrypted. Use your demo account on trusted Wi-Fi.</Banner>}
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
