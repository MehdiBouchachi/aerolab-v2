import { CameraView, useCameraPermissions } from 'expo-camera'
import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { ActivityIndicator, Pressable, ScrollView, Text, TextInput, View } from 'react-native'
import { ApiFailure, freshnessLabel, type Reading } from '../core/session'
import { newChallenge, parsePairingPayload, PairingError, verifyIdentity, acceptableHint } from '../core/identity'
import type { Mode } from '../core/types'
import { useApp } from './AppState'
import { c, s } from './theme'

// Minimal response shapes (the edge/cloud APIs are the source of truth: contracts/openapi).
interface Unit { id: string; name: string; connection: string; control_state: string | null; pump_commanded: string | null; applied_program_version: number | null; is_simulated: boolean; site_stale?: boolean
  latest_readings: { effective_at_utc: string; outdated: boolean; readings: Record<string, { value: number | boolean | null; quality: string; unit?: string }> } | null }
interface Summary { units: { total: number; healthy: number; attention: number; stale: number }; active_incidents: { critical: number; warning: number }; site_stale?: boolean }
interface Incident { id: string; unit_id: string; severity: string; title: string; suggested_action: string; status: string }

export const Btn = ({ label, onPress, primary, disabled }: { label: string; onPress: () => void; primary?: boolean; disabled?: boolean }) => (
  <Pressable accessibilityRole="button" accessibilityLabel={label} disabled={disabled} onPress={onPress} style={[s.btn, primary && s.primary, disabled && { opacity: 0.5 }]}><Text style={[s.text, primary && s.primaryText]}>{label}</Text></Pressable>
)

const REASONS: Record<string, string> = {
  LOCAL_UNREACHABLE: 'The site is not reachable from here.', LOCAL_IDENTITY_MISMATCH: 'A device answered at the site address but is NOT your paired AeroLab Pi. It was ignored.',
  LOCAL_LOGIN_REQUIRED: 'Sign in to the site to see live data.', LOCAL_ONLY_MODE_NO_LOCAL: '“Local only” is on and the site is not reachable. The cloud is not used.',
  CLOUD_ONLY_MODE_NO_CLOUD: 'No cloud address is set.', CLOUD_LOGIN_REQUIRED: 'Sign in to the cloud to see data.', CLOUD_UNREACHABLE: 'The cloud is not reachable.', NO_SOURCE_AVAILABLE: 'Neither the site nor the cloud is reachable.',
}

/** The source badge is on every operational screen (spec 8.8). */
export function SourceBadge() {
  const { decision, busy } = useApp()
  if (!decision) return <Text style={s.muted}>Connecting…</Text>
  if (decision.kind === 'live') return (
    <View style={[s.banner, s.okB]}><Text style={s.text}>{decision.source === 'local' ? 'LOCAL · connected to the site' : 'CLOUD · synchronised copy (can be behind the site)'}{decision.insecureLab ? ' · unencrypted lab link' : ''}{busy ? ' …' : ''}</Text></View>)
  return (
    <View style={[s.banner, decision.kind === 'cached' ? s.warnB : s.critB]}>
      <Text style={s.text}>{decision.kind === 'cached' ? `CACHED · read only${decision.cachedFrom ? ` (${decision.cachedFrom})` : ''}` : 'NOT CONNECTED'}</Text>
      <Text style={s.muted}>{REASONS[decision.reason] ?? decision.reason}</Text>
      {decision.kind === 'cached' && decision.cachedAt && <Text style={s.muted}>Data from {new Date(decision.cachedAt).toLocaleString()}. Controls are disabled.</Text>}
    </View>)
}

/** Loads one resource from the CURRENT source; falls back to that source's own cache, clearly marked. */
function useResource<T>(path: string | null, deps: unknown[] = []) {
  const { session, decision } = useApp()
  const [r, setR] = useState<Reading<T> | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const load = useCallback(async () => {
    if (!path || !decision) return
    try {
      if (decision.kind === 'live') { setR(await session.get<T>(path)); setErr(null) }
      else { setR(await session.cached<T>(path)); setErr(null) }
    } catch (e) {
      if (e instanceof ApiFailure && e.code === 'SUPERSEDED') return
      setErr(e instanceof ApiFailure ? e.message : 'Failed to load')
      setR(await session.cached<T>(path)) // keep showing the last good copy, flagged as cached
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, decision, session, ...deps])
  useEffect(() => { setR(null); void load(); const t = setInterval(() => void load(), 10000); return () => clearInterval(t) }, [load])
  return { r, err, reload: load }
}

const Fresh = ({ r }: { r: Reading<unknown> | null }) => (r ? <Text style={s.muted}>{freshnessLabel(r, Date.now())}</Text> : null)
const Screen = ({ children }: { children: ReactNode }) => <ScrollView style={s.screen} contentContainerStyle={s.pad}>{children}</ScrollView>

export function Home({ go }: { go: (to: 'unit' | 'pair' | 'login' | 'settings', arg?: string) => void }) {
  const { pairing, pairings, selectSite, decision } = useApp()
  const site = pairing?.siteId ?? ''
  const sum = useResource<Summary>(pairing ? `/api/v1/sites/${site}/summary` : null)
  const units = useResource<{ units: Unit[] }>(pairing ? `/api/v1/sites/${site}/units?limit=100` : null)
  const inc = useResource<{ incidents: Incident[] }>(pairing ? `/api/v1/sites/${site}/incidents?state=active&limit=10` : null)
  if (!pairing) return <Screen><Text style={s.h1}>AeroLab</Text><Text style={s.text}>Pair this phone with your site to get started.</Text><Btn primary label="Pair a site" onPress={() => go('pair')} /></Screen>
  const stale = sum.r?.data.site_stale
  return (
    <Screen>
      <View style={s.row}><Text style={s.h1}>{pairing.name}</Text><Btn label="Settings" onPress={() => go('settings')} /></View>
      {pairings.length > 1 && <ScrollView horizontal>{pairings.map(p => <Btn key={p.siteId} label={p.name} primary={p.siteId === site} onPress={() => void selectSite(p.siteId)} />)}</ScrollView>}
      <SourceBadge />
      {decision && decision.kind !== 'live' && decision.needsLocalLogin && <Btn primary label="Sign in to the site" onPress={() => go('login')} />}
      {decision?.kind === 'unavailable' && decision.reason === 'CLOUD_LOGIN_REQUIRED' && <Btn primary label="Sign in to the cloud" onPress={() => go('login')} />}
      {decision?.kind === 'live' && !sum.r && !sum.err && <ActivityIndicator />}
      {stale && <View style={[s.banner, s.warnB]}><Text style={s.text}>The site has not synced recently. Values are the last known and may be out of date; this does not prove the pumps stopped.</Text></View>}
      {sum.err && <View style={[s.banner, s.warnB]}><Text style={s.text}>{sum.err}</Text></View>}
      {sum.r && (<>
        <View style={s.row}>
          <View style={s.tile}><Text style={s.h1}>{sum.r.data.units.total}</Text><Text style={s.muted}>units</Text></View>
          <View style={s.tile}><Text style={s.h1}>{sum.r.data.units.healthy}</Text><Text style={s.muted}>running</Text></View>
          <View style={[s.tile, sum.r.data.units.attention > 0 && { borderColor: c.warn }]}><Text style={s.h1}>{sum.r.data.units.attention}</Text><Text style={s.muted}>attention</Text></View>
        </View>
        <Fresh r={sum.r} />
      </>)}
      {inc.r && inc.r.data.incidents.length > 0 && <Text style={s.h2}>Active alerts</Text>}
      {inc.r?.data.incidents.map(i => (
        <View key={i.id} style={[s.banner, i.severity === 'critical' ? s.critB : s.warnB]}><Text style={s.text}>{i.severity === 'critical' ? '■ ' : '▲ '}{i.title} — {i.unit_id}</Text><Text style={s.muted}>{i.suggested_action}</Text></View>))}
      <Text style={s.h2}>Growing units</Text>
      {units.r?.data.units.map(u => (
        <Pressable key={u.id} accessibilityRole="button" onPress={() => go('unit', u.id)} style={s.card}>
          <Text style={s.h2}>{u.name}{u.is_simulated ? '  · SIMULATED' : ''}</Text>
          <Text style={s.text}>{u.connection !== 'ONLINE' ? '▲ Not reporting' : u.control_state === 'RUNNING' ? '✔ Running' : u.control_state === 'PROTECTED_STOP' ? '■ Protected stop' : (u.control_state ?? 'unknown')}</Text>
          <Text style={s.muted}>Pump {u.pump_commanded ? `commanded ${u.pump_commanded}` : 'unknown'} · programme {u.applied_program_version ?? 'none'}</Text>
        </Pressable>))}
      {units.r && units.r.data.units.length === 0 && <Text style={s.muted}>No units are registered at this site yet.</Text>}
    </Screen>)
}

export function UnitScreen({ id, back }: { id: string; back: () => void }) {
  const { pairing, decision, session } = useApp()
  const site = pairing!.siteId
  const unit = useResource<{ unit: Unit }>(`/api/v1/sites/${site}/units/${id}`)
  const u = unit.r?.data.unit
  const label = (k: string) => ({ air_temp_c: 'Air temperature', relative_humidity_pct: 'Humidity', water_temp_c: 'Water temperature', ph: 'pH', ec_ms_cm: 'EC', reservoir_level_ok: 'Reservoir level', light_lux: 'Light' }[k] ?? k)
  const show = (v: { value: number | boolean | null; quality: string; unit?: string }) => (v.quality === 'missing' ? 'no sensor' : v.quality === 'invalid' ? 'invalid' : v.value === null ? '–' : typeof v.value === 'boolean' ? (v.value ? 'OK' : 'LOW') : `${v.value}${v.unit ? ' ' + v.unit : ''}`)
  void session
  return (
    <Screen>
      <Btn label="← Back" onPress={back} />
      <SourceBadge />
      <Text style={s.h1}>{u?.name ?? id}</Text>
      {unit.err && <Text style={s.muted}>{unit.err}</Text>}
      {u && (<>
        <Text style={s.text}>{u.connection === 'ONLINE' ? `Controller: ${u.control_state}` : '▲ Controller not reporting – last known state, may be out of date'}</Text>
        <Text style={s.muted}>Pump {u.pump_commanded ? `commanded ${u.pump_commanded}` : 'unknown'} · water delivery not verified · programme {u.applied_program_version ?? 'none'}</Text>
        <Fresh r={unit.r} />
        <Text style={s.h2}>Readings</Text>
        {u.latest_readings ? (<>
          <Text style={s.muted}>{u.latest_readings.outdated ? 'OUTDATED – ' : ''}taken {new Date(u.latest_readings.effective_at_utc).toLocaleTimeString()}</Text>
          {Object.entries(u.latest_readings.readings).map(([k, v]) => (<View key={k} style={s.row}><Text style={s.text}>{label(k)}</Text><Text style={[s.text, v.quality !== 'valid' && { color: c.muted }]}>{show(v)}</Text></View>))}
        </>) : <Text style={s.muted}>No readings stored yet.</Text>}
        <Text style={s.muted}>{decision?.kind === 'live' ? 'Programme changes are made from the web dashboard.' : 'Read only while not connected.'}</Text>
      </>)}
    </Screen>)
}

export function PairScreen({ back }: { back: () => void }) {
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
      let paired = false
      for (const hint of p.hints) {
        const h = acceptableHint(hint); if (!h) continue
        // 1) verify the Pi BEFORE sending it the code: it must prove it holds the key printed in the QR
        const challenge = newChallenge(env.random)
        const idr = await env.fetch(`${h.url}/api/v1/identity?challenge=${encodeURIComponent(challenge)}`).then(r => r.json()).catch(() => null)
        if (!idr || !verifyIdentity({ siteId: p.site, publicKey: p.pk }, challenge, idr)) continue
        // 2) consume the single-use code; the answer must repeat the pinned key
        const r = await env.fetch(`${h.url}/api/v1/site-pairing/confirm`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: p.code, device_name: 'AeroLab phone' }) })
        if (!r.ok) { setMsg(r.status === 401 ? 'That pairing code is invalid or expired. Ask an administrator for a new one.' : 'Pairing failed.'); setBusy(false); return }
        const body = await r.json() as { site_id: string; public_key: string }
        if (body.site_id !== p.site || body.public_key !== p.pk) { setMsg('The site did not match the pairing code.'); setBusy(false); return }
        await addPairing({ siteId: p.site, name: p.name, publicKey: p.pk, localHints: p.hints, pairedAt: Date.now() }); paired = true; break
      }
      if (!paired && !msg) setMsg('Could not verify the site. Are you on the site Wi-Fi? Nothing was sent.')
      else if (paired) back()
    } catch (e) { setMsg(e instanceof PairingError ? e.message : 'Pairing failed.') }
    setBusy(false)
  }, [env, addPairing, back, msg])
  return (
    <Screen>
      <Btn label="← Back" onPress={back} />
      <Text style={s.h1}>Pair a site</Text>
      <Text style={s.muted}>On the site, an administrator opens Administration → Pair a phone to show a QR code (valid 5 minutes, single use). Join the site Wi-Fi first.</Text>
      {!manual && (perm?.granted
        ? <View style={{ height: 300, borderRadius: 8, overflow: 'hidden' }}><CameraView style={{ flex: 1 }} barcodeScannerSettings={{ barcodeTypes: ['qr'] }} onBarcodeScanned={e => { if (!busy) void doPair(e.data) }} /></View>
        : <Btn primary label="Allow camera to scan the QR code" onPress={() => void ask()} />)}
      <Btn label={manual ? 'Use the camera' : 'Enter the pairing text manually'} onPress={() => setManual(!manual)} />
      {manual && (<><TextInput style={[s.input, { minHeight: 120 }]} multiline value={payload} onChangeText={setPayload} placeholder="Paste the pairing text" accessibilityLabel="Pairing text" /><Btn primary label="Pair" disabled={busy || !payload} onPress={() => void doPair(payload)} /></>)}
      {busy && <ActivityIndicator />}
      {msg && <View style={[s.banner, s.critB]}><Text style={s.text}>{msg}</Text></View>}
    </Screen>)
}

export function LoginScreen({ back }: { back: () => void }) {
  const { decision, pairing, signedIn, env } = useApp()
  const wantsCloud = decision?.kind === 'unavailable' && decision.reason === 'CLOUD_LOGIN_REQUIRED'
  const [source, setSource] = useState<'local' | 'cloud'>(wantsCloud ? 'cloud' : 'local')
  const [u, setU] = useState(''); const [p, setP] = useState(''); const [err, setErr] = useState<string | null>(null); const [busy, setBusy] = useState(false)
  const submit = async () => {
    if (!pairing) return
    setBusy(true); setErr(null)
    try {
      let base = pairing.cloudUrl ?? ''
      if (source === 'local') {
        // find the VERIFIED local address again: credentials only ever go to the paired Pi
        const { probeLocal } = await import('../core/connection'); const pr = await probeLocal(env, pairing)
        if (!pr.ok) { setErr(pr.reason === 'LOCAL_IDENTITY_MISMATCH' ? 'That is not your paired Pi. Nothing was sent.' : 'The site is not reachable.'); setBusy(false); return }
        base = pr.baseUrl
      } else if (!base) { setErr('Set the cloud address in Settings first.'); setBusy(false); return }
      const r = await env.fetch(base + (source === 'local' ? '/api/v1/local/auth/login' : '/api/v1/auth/login'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(source === 'local' ? { username: u, password: p } : { email: u, password: p }) })
      if (r.status === 401) setErr('That username and password do not match.'); else if (r.status === 429) setErr('Too many attempts. Wait a few minutes.'); else if (!r.ok) setErr('Sign-in failed.')
      else { const b = await r.json() as { token: string }; await signedIn(source, u.toLowerCase(), b.token); back() }
    } catch { setErr('Could not reach the server.') }
    setBusy(false)
  }
  return (
    <Screen>
      <Btn label="← Back" onPress={back} />
      <Text style={s.h1}>Sign in</Text>
      <View style={s.row}><Btn label="Site (local)" primary={source === 'local'} onPress={() => setSource('local')} /><Btn label="Cloud" primary={source === 'cloud'} onPress={() => setSource('cloud')} /></View>
      <Text style={s.muted}>{source === 'local' ? 'Local accounts work with no internet and are separate from cloud accounts.' : 'Cloud accounts are separate from site accounts.'}</Text>
      <TextInput style={s.input} value={u} onChangeText={setU} autoCapitalize="none" autoCorrect={false} placeholder={source === 'cloud' ? 'Email' : 'Username'} accessibilityLabel={source === 'cloud' ? 'Email' : 'Username'} />
      <TextInput style={s.input} value={p} onChangeText={setP} secureTextEntry placeholder="Password" accessibilityLabel="Password" />
      {err && <View style={[s.banner, s.critB]}><Text style={s.text}>{err}</Text></View>}
      <Btn primary label={busy ? 'Signing in…' : 'Sign in'} disabled={busy || !u || !p} onPress={() => void submit()} />
    </Screen>)
}

export function SettingsScreen({ back, pair }: { back: () => void; pair: () => void }) {
  const { mode, setMode, pairing, removePairing, setCloudUrl, decision } = useApp()
  const [cloud, setCloud] = useState(pairing?.cloudUrl ?? '')
  const opts: [Mode, string, string][] = [['auto', 'Automatic', 'Use the site when reachable, otherwise the cloud.'], ['local', 'Local only', 'Only the site Wi-Fi. Never the cloud.'], ['cloud', 'Cloud only', 'Only the cloud. Never the local network.']]
  return (
    <Screen>
      <Btn label="← Back" onPress={back} />
      <Text style={s.h1}>Connection</Text>
      <SourceBadge />
      {opts.map(([m, l, d]) => (<Pressable key={m} accessibilityRole="radio" accessibilityState={{ selected: mode === m }} onPress={() => void setMode(m)} style={[s.card, mode === m && { borderColor: c.green, borderWidth: 2 }]}><Text style={s.h2}>{mode === m ? '● ' : '○ '}{l}</Text><Text style={s.muted}>{d}</Text></Pressable>))}
      {pairing && (<>
        <Text style={s.h2}>Cloud address (https)</Text>
        <TextInput style={s.input} value={cloud} onChangeText={setCloud} autoCapitalize="none" placeholder="https://…" accessibilityLabel="Cloud address" />
        <Btn label="Save cloud address" onPress={() => { const h = acceptableHint(cloud); if (cloud && (!h || h.insecureLab)) return; void setCloudUrl(h?.url ?? '') }} />
        <Text style={s.muted}>Switching source starts a fresh connection: nothing from the other source is shown.{decision?.kind === 'live' && decision.insecureLab ? ' This link is unencrypted (private network).' : ''}</Text>
        <Btn label="Pair another site" onPress={pair} />
        <Btn label={`Unpair ${pairing.name}`} onPress={() => void removePairing(pairing.siteId)} />
      </>)}
    </Screen>)
}
