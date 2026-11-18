import { useRef, useState, type ReactNode } from 'react'
import { KeyboardAvoidingView, Modal, Platform, ScrollView, View } from 'react-native'
import { random } from '../native'
import { useApp } from './AppState'
import { Badge, Banner, Btn, Loading, PageTitle, Screen, Segmented } from './components'
import { Text, TextInput } from './fonts'
import { usePaths, useResource, useSiteCapabilities, useSource } from './hooks'
import { useTheme } from './theme'

type Tab = 'storage' | 'controllers' | 'users' | 'activity' | 'cloud'
type Role = 'VIEWER' | 'OPERATOR' | 'ADMIN'
type LocalUser = { id: string; username: string; role: Role; disabled: boolean }
type Member = { user_id: string; email: string; role: Role }
type Device = { device_id: string; unit_id: string; data_source: string; connection: string }
type Event = { id: string; action: string; target_type: string; occurred_at: string }
type Storage = {
  status: string; durable_receipts_enabled: boolean; database_bytes: number; wal_bytes: number
  disk_free_bytes: number; disk_total_bytes: number; telemetry_rows: number; retention_days: number
  cloud_sync: { enabled: boolean; outbox_pending: number }
}
type CloudLink = { link: { linked: boolean; organization_name?: string }; sync: { last_sync_at?: string | null } }
const bytes = (value: number) => value >= 1073741824 ? `${(value / 1073741824).toFixed(1)} GiB` : `${(value / 1048576).toFixed(1)} MiB`
const words = (value: string) => value.replace(/_/g, ' ').toLowerCase()

export function AdministrationScreen() {
  const { decision, pairing } = useApp()
  const permission = useSiteCapabilities()
  const allowed = decision?.kind === 'live' && !!permission.r?.data.capabilities.includes('manage_users')
  if (!allowed) return <Screen>
    <PageTitle title="Administration" />
    {permission.loading ? <Loading what="access" /> : <Banner tone="muted">{decision?.kind === 'live' ? permission.err ?? 'Administrator access is required.' : 'Connect to the site or cloud to open Administration.'}</Banner>}
  </Screen>
  return <AdminViews key={`${pairing?.siteId}|${decision.source}`} />
}

function AdminViews() {
  const { s, c } = useTheme()
  const p = usePaths()
  const local = useSource() === 'local'
  const [tab, setTab] = useState<Tab>('storage')
  const [query, setQuery] = useState('')
  const [adding, setAdding] = useState(false)
  const storage = useResource<Storage>(tab === 'storage' ? p.storage : null)
  const devices = useResource<{ devices: Device[] }>(tab === 'controllers' ? p.controllers : null)
  const users = useResource<{ users?: LocalUser[]; members?: Member[] }>(tab === 'users' ? p.users : null)
  const activity = useResource<{ events: Event[] }>(tab === 'activity' ? p.activity : null)
  const cloud = useResource<CloudLink>(tab === 'cloud' && local ? p.cloudLink : null)
  const resource = { storage, controllers: devices, users, activity, cloud }[tab]
  const tabs: [Tab, string][] = [['storage', 'Storage'], ['controllers', 'Controllers'], ['users', 'Users'], ['activity', 'Activity']]
  if (local) tabs.push(['cloud', 'Cloud connection'])
  const accounts = [
    ...(users.r?.data.users ?? []).map(u => ({ id: u.id, name: u.username, role: u.role, status: u.disabled ? 'Disabled' : 'Active' })),
    ...(users.r?.data.members ?? []).map(u => ({ id: u.user_id, name: u.email, role: u.role, status: '' })),
  ].filter(u => u.name.toLowerCase().includes(query.trim().toLowerCase()))
  const disk = storage.r?.data

  return <Screen onRefresh={() => void resource.reload()} refreshing={resource.loading && !!resource.r}>
    <PageTitle title="Administration" />
    <Segmented value={tab} options={tabs} onChange={setTab} label="Administration sections" />
    {resource.err && <Banner tone="warn">{resource.err}</Banner>}
    {resource.r?.fromCache && <Text style={s.small}>Last known data</Text>}
    {!resource.r && resource.loading && <Loading what={tab} />}
    {tab === 'storage' && disk && <View style={s.panel}>
      <AdminRow title={disk.status === 'OK' && disk.durable_receipts_enabled ? 'Storage operating normally' : 'Storage needs attention'}
        detail={!disk.durable_receipts_enabled ? 'New records are not being saved or acknowledged.' : undefined} />
      <AdminRow bordered title="Database" value={bytes(disk.database_bytes)} detail={`Log: ${bytes(disk.wal_bytes)}`} />
      <AdminRow bordered title="Free disk" value={bytes(disk.disk_free_bytes)} detail={`of ${bytes(disk.disk_total_bytes)}`} />
      <AdminRow bordered title="Stored snapshots" value={disk.telemetry_rows.toLocaleString()} detail={`Retained ${disk.retention_days} days`} />
      <AdminRow bordered title="Cloud sync" value={disk.cloud_sync.enabled ? `${disk.cloud_sync.outbox_pending} waiting` : 'Off'} />
    </View>}
    {tab === 'controllers' && devices.r && <View style={s.panel}>
      {devices.r.data.devices.length === 0 && <AdminRow title="No controllers registered" />}
      {devices.r.data.devices.map((d, i) => <AdminRow key={d.device_id} bordered={i > 0} title={d.device_id}
        detail={`${d.unit_id} · ${d.data_source === 'SIMULATOR' ? 'Simulated' : 'Real hardware'}`}>
        <Badge tone={d.connection === 'ONLINE' ? 'ok' : 'warn'}>{words(d.connection)}</Badge>
      </AdminRow>)}
    </View>}
    {tab === 'users' && <>
      <TextInput style={s.input} value={query} onChangeText={setQuery} placeholder="Search users" placeholderTextColor={c.muted} accessibilityLabel="Search users" />
      {local && <Btn kind="primary" label="Add user" onPress={() => setAdding(true)} />}
      {adding && local && <NewUserModal onClose={() => setAdding(false)} onCreated={() => void users.reload()} />}
      {users.r && <View style={s.panel}>
        {!accounts.length && <AdminRow title={query ? 'No matching users' : 'No users found'} />}
        {accounts.map((u, i) => <AdminRow key={u.id} bordered={i > 0} title={u.name} value={words(u.role)} detail={u.status} />)}
      </View>}
    </>}
    {tab === 'activity' && activity.r && <View style={s.panel}>
      {!activity.r.data.events.length && <AdminRow title="No recent activity" />}
      {activity.r.data.events.map((e, i) => <AdminRow key={e.id} bordered={i > 0} title={words(e.action)}
        detail={[new Date(e.occurred_at).toLocaleString(), words(e.target_type ?? '')].filter(Boolean).join(' · ')} />)}
    </View>}
    {tab === 'cloud' && cloud.r && <View style={s.panel}>
      <AdminRow title={cloud.r.data.link.linked ? 'Connected to the cloud' : 'Cloud link is off'} detail={cloud.r.data.link.organization_name} />
      {cloud.r.data.sync.last_sync_at && <AdminRow bordered title="Last sync" detail={new Date(cloud.r.data.sync.last_sync_at).toLocaleString()} />}
    </View>}
  </Screen>
}

function AdminRow({ title, detail, value, bordered, children }: { title: string; detail?: string; value?: string; bordered?: boolean; children?: ReactNode }) {
  const { s, c } = useTheme()
  return <View style={[s.between, { padding: 12, borderTopWidth: bordered ? 1 : 0, borderTopColor: c.line, flexWrap: 'wrap' }]}>
    <View style={{ flexGrow: 1, flexShrink: 1, gap: 2 }}><Text style={s.strong}>{title}</Text>{!!detail && <Text style={s.small}>{detail}</Text>}</View>
    {!!value && <Text style={s.muted}>{value}</Text>}{children}
  </View>
}

function NewUserModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const { c, s } = useTheme()
  const { session, decision } = useApp()
  const p = usePaths()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<Role>('VIEWER')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const pending = useRef(false)
  const close = () => { if (!pending.current) onClose() }
  const create = async () => {
    if (pending.current || decision?.kind !== 'live' || decision.source !== 'local') return
    if (username.trim().length < 3 || password.length < 12) {
      setError('Use at least 3 characters for the username and 12 for the password.')
      return
    }
    pending.current = true
    setBusy(true)
    setError('')
    try {
      const key = Array.from(random(16), b => b.toString(16).padStart(2, '0')).join('')
      await session.post(p.users, { username: username.trim(), password, role }, key, true)
      onCreated()
      onClose()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not create the account.')
    } finally {
      pending.current = false
      setBusy(false)
    }
  }
  return <Modal transparent onRequestClose={close}>
    <KeyboardAvoidingView behavior={Platform.OS === 'ios' ? 'padding' : undefined} style={{ flex: 1, justifyContent: 'center', padding: 16, backgroundColor: c.backdrop }}>
      <View accessibilityViewIsModal style={[s.panel, { width: '100%', maxWidth: 480, maxHeight: '88%', alignSelf: 'center' }]}>
        <Text accessibilityRole="header" style={[s.h2, { padding: 14 }]}>New user</Text>
        <ScrollView keyboardShouldPersistTaps="handled" contentContainerStyle={{ gap: 12, paddingHorizontal: 14, paddingBottom: 14 }}>
          {!!error && <Banner tone="crit">{error}</Banner>}
          <View><Text style={s.fieldLabel}>Username</Text><TextInput style={s.input} value={username} onChangeText={setUsername} autoCapitalize="none" autoCorrect={false} editable={!busy} accessibilityLabel="Username" /></View>
          <View><Text style={s.fieldLabel}>Password (12+ characters)</Text><TextInput style={s.input} value={password} onChangeText={setPassword} secureTextEntry autoComplete="new-password" editable={!busy} accessibilityLabel="Password" /></View>
          <View><Text style={s.fieldLabel}>Role</Text><Segmented label="Role" value={role} onChange={value => { if (!busy) setRole(value) }}
            options={([['VIEWER', 'Viewer'], ['OPERATOR', 'Operator'], ['ADMIN', 'Admin']] as [Role, string][])} /></View>
        </ScrollView>
        <View style={[s.row, { padding: 12, borderTopWidth: 1, borderTopColor: c.line, justifyContent: 'flex-end', flexWrap: 'wrap' }]}>
          <Btn label="Cancel" disabled={busy} onPress={close} />
          <Btn kind="primary" label={busy ? 'Creating…' : 'Create user'} disabled={busy} onPress={() => void create()} />
        </View>
      </View>
    </KeyboardAvoidingView>
  </Modal>
}
