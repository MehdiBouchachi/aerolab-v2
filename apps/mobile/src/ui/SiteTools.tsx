import { useRef, useState } from 'react'
import { KeyboardAvoidingView, Modal, Platform, ScrollView, View } from 'react-native'
import { random } from '../native'
import { useApp } from './AppState'
import { Banner, Btn, EmptyState, Loading, PageTitle, Screen } from './components'
import { label, type CropProfile } from './format'
import { Text, TextInput } from './fonts'
import { usePaths, useResource, useSiteCapabilities } from './hooks'
import { useTheme } from './theme'

const PAGE_SIZE = 12

export function CropProfilesScreen() {
  const { pairing } = useApp()
  const { s, c } = useTheme()
  const paths = usePaths()
  const profiles = useResource<{ items: CropProfile[] }>(pairing ? paths.crops : null)
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(0)
  const [expanded, setExpanded] = useState<string | null>(null)
  const items = profiles.r?.data.items ?? []
  const search = query.trim().toLowerCase()
  const results = items.filter(p => [p.name, p.crop, p.variety].some(value => value?.toLowerCase().includes(search)))
  const lastPage = Math.max(0, Math.ceil(results.length / PAGE_SIZE) - 1)
  const current = Math.min(page, lastPage)
  return <Screen refreshing={profiles.loading && !!profiles.r} onRefresh={pairing ? () => void profiles.reload() : undefined}>
    <PageTitle title="Crop profiles" />
    {!pairing && <EmptyState icon="overview" title="No site paired">Pair a site from Account.</EmptyState>}
    {profiles.err && <Banner tone="warn">{profiles.err}</Banner>}
    {profiles.r?.fromCache && <Text style={s.small}>Cached profiles</Text>}
    {!profiles.r && profiles.loading && <Loading what="crop profiles" />}
    {!!items.length && <TextInput style={s.input} value={query} onChangeText={value => { setQuery(value); setPage(0) }}
      accessibilityLabel="Search crop profiles" placeholder="Search crop profiles" placeholderTextColor={c.muted} />}
    {profiles.r && !results.length && <EmptyState icon="overview" title={search ? 'No matches' : 'No crop profiles'}>{search ? 'Try a different name or crop.' : 'Profiles created on the web appear here.'}</EmptyState>}
    {!!results.length && <View style={s.panel}>
      {results.slice(current * PAGE_SIZE, (current + 1) * PAGE_SIZE).map((p, i) => <View key={p.id} style={{ padding: 12, gap: 4, borderTopWidth: i ? 1 : 0, borderTopColor: c.line }}>
        <View style={s.between}><Text style={[s.strong, { flex: 1 }]}>{p.name}</Text><Text style={s.small}>{p.unit_count} {p.unit_count === 1 ? 'unit' : 'units'}</Text></View>
        <Text style={s.muted}>{[p.crop, p.variety, `v${p.version}`].filter(Boolean).join(' · ')}</Text>
        <Btn kind="link" label={expanded === p.id ? 'Hide details' : 'Ranges and programme'} onPress={() => setExpanded(id => id === p.id ? null : p.id)} />
        {expanded === p.id && <View style={{ gap: 5 }}>
          {Object.entries(p.thresholds).map(([sensor, range]) => <View key={sensor} style={s.between}>
            <Text style={s.muted}>{label(sensor)}</Text><Text style={s.text}>{range.min ?? '–'} to {range.max ?? '–'}</Text>
          </View>)}
          {p.program && <Text style={s.text}>Pump on {p.program.on_duration_seconds}s · off {p.program.off_duration_seconds}s · max {p.program.max_continuous_on_seconds}s</Text>}
          <Text style={s.small}>Source: {p.value_source}</Text>
          {p.notes && <Text style={s.muted}>{p.notes}</Text>}
        </View>}
      </View>)}
    </View>}
    {lastPage > 0 && <View style={s.between}>
      <Btn label="Previous" disabled={current === 0} onPress={() => setPage(current - 1)} />
      <Text style={s.small}>{current + 1} / {lastPage + 1}</Text>
      <Btn label="Next" disabled={current === lastPage} onPress={() => setPage(current + 1)} />
    </View>}
  </Screen>
}

type Setup = { setup_code: string; setup: { unit_id: string }; shown_once: string; note?: string; warning?: string }

export function AddUnitModal({ onClose, onCreated, onOpen }: {
  onClose: () => void; onCreated: () => void; onOpen: (id: string) => void
}) {
  const { c, s } = useTheme()
  const { session, decision } = useApp()
  const paths = usePaths()
  const profiles = useResource<{ items: CropProfile[] }>(paths.crops, 0)
  const permissions = useSiteCapabilities()
  const [name, setName] = useState('')
  const [crop, setCrop] = useState<string | null>(null)
  const [picking, setPicking] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [setup, setSetup] = useState<Setup | null>(null)
  const submitting = useRef(false)
  const canCreate = decision?.kind === 'live' && !!permissions.r?.data.capabilities.includes('manage_devices')
  const close = () => { if (!submitting.current) onClose() }
  const submit = async () => {
    if (!canCreate || setup || submitting.current || !name.trim()) return
    submitting.current = true
    setBusy(true)
    setError('')
    try {
      setSetup(await session.post<Setup>(paths.addUnit, { name: name.trim(), ...(crop ? { crop_profile_id: crop } : {}) }, Array.from(random(16), n => n.toString(16).padStart(2, '0')).join(''), true))
      onCreated()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not add the unit.')
    } finally {
      submitting.current = false
      setBusy(false)
    }
  }

  const options = profiles.r?.data.items ?? []
  return <Modal transparent onRequestClose={close}>
    <KeyboardAvoidingView behavior={Platform.OS === 'ios' ? 'padding' : undefined}
      style={{ flex: 1, padding: 16, justifyContent: 'center', backgroundColor: c.backdrop }}>
      <View accessibilityViewIsModal style={[s.panel, { width: '100%', maxWidth: 480, maxHeight: '88%', alignSelf: 'center' }]}>
        <Text accessibilityRole="header" style={[s.h2, { padding: 14 }]}>{setup ? 'Set up the controller' : 'Add a unit'}</Text>
        <ScrollView keyboardShouldPersistTaps="handled" contentContainerStyle={{ paddingHorizontal: 14, paddingBottom: 14, gap: 10 }}>
          {error && <Banner tone="crit">{error}</Banner>}
          {setup ? <>
            <Text style={s.muted}>{setup.shown_once}</Text>
            {setup.warning && <Banner tone="warn">{setup.warning}</Banner>}
            {setup.note && <Text style={s.muted}>{setup.note}</Text>}
            <Text style={s.text}>Join the controller’s AeroLab-Setup Wi-Fi. Open 192.168.4.1 and paste this code.</Text>
            <Text selectable accessibilityLabel="Controller setup code" style={[s.text, { backgroundColor: c.bg, padding: 10 }]}>{setup.setup_code}</Text>
          </> : <>
            <View><Text style={s.fieldLabel}>Unit name</Text>
              <TextInput style={s.input} value={name} onChangeText={setName} maxLength={80} editable={!busy} accessibilityLabel="Unit name" />
            </View>
            <View><Text style={s.fieldLabel}>Crop profile (optional)</Text>
              <Btn label={options.find(p => p.id === crop)?.name ?? 'No crop profile'} disabled={busy || !options.length} onPress={() => setPicking(v => !v)} />
              {picking && <>
                <Btn kind="link" label="No crop profile" onPress={() => { setCrop(null); setPicking(false) }} />
                {options.map(p => <Btn key={p.id} kind="link" label={p.name} onPress={() => { setCrop(p.id); setPicking(false) }} />)}
              </>}
            </View>
            {profiles.err && <Text style={s.small}>Crop profiles are unavailable. You can add the unit without one.</Text>}
          </>}
        </ScrollView>
        <View style={[s.row, { justifyContent: 'flex-end', flexWrap: 'wrap', padding: 12, borderTopWidth: 1, borderTopColor: c.line }]}>
          <Btn label={setup ? 'Close' : 'Cancel'} disabled={busy} onPress={close} />
          {setup ? <Btn kind="primary" label="Open unit" onPress={() => onOpen(setup.setup.unit_id)} />
            : <Btn kind="primary" label={busy ? 'Adding…' : 'Add unit'} disabled={busy || !name.trim() || !canCreate} onPress={() => void submit()} />}
        </View>
      </View>
    </KeyboardAvoidingView>
  </Modal>
}
