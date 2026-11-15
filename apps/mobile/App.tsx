import { StatusBar } from 'expo-status-bar'
import { useCallback, useEffect, useState } from 'react'
import { BackHandler, KeyboardAvoidingView, Platform, View } from 'react-native'
import { SafeAreaProvider } from 'react-native-safe-area-context'
import { AppProvider, useApp } from './src/ui/AppState'
import { StatusStrip, TabBar, TopBar, type Tab } from './src/ui/components'
import { ago, type Summary } from './src/ui/format'
import { usePaths, useResource } from './src/ui/hooks'
import { AccountScreen, AlertsScreen, LoginScreen, Overview, PairScreen, SummaryCtx, UnitScreen, type Go } from './src/ui/screens'
import { ThemeProvider, useTheme } from './src/ui/theme'

// Same structure as the web on phones: top bar, status strip, content, bottom tabs.
type Pushed = { name: 'unit'; id: string } | { name: 'pair' } | { name: 'login' }

function Shell() {
  const { c, scheme } = useTheme()
  const { pairing, decision } = useApp()
  const p = usePaths()
  const [tab, setTab] = useState<Tab>('overview')
  const [stack, setStack] = useState<Pushed[]>([])
  const top = stack[stack.length - 1]
  const back = useCallback(() => setStack(st => st.slice(0, -1)), [])
  const go: Go = useCallback((to, arg) => {
    if (to === 'alerts' || to === 'account') { setStack([]); setTab(to); return }
    setStack(st => [...st, to === 'unit' ? { name: 'unit', id: arg! } : { name: to }])
  }, [])

  // Android hardware back pops the stack, then returns to Overview, then leaves the app.
  useEffect(() => {
    const sub = BackHandler.addEventListener('hardwareBackPress', () => {
      if (stack.length) { back(); return true }
      if (tab !== 'overview') { setTab('overview'); return true }
      return false
    })
    return () => sub.remove()
  }, [stack.length, tab, back])

  const sum = useResource<Summary>(pairing ? p.summary : null)
  const d = sum.r?.data
  const statusItems = !pairing ? [] : decision?.kind === 'live'
    ? [{ icon: 'activity' as const, text: decision.source === 'local' ? 'Site · connected' : 'Cloud copy' }, { icon: 'clock' as const, text: sum.r ? `Updated ${ago(sum.r.fetchedAt)}` : 'Loading…' }]
    : decision?.kind === 'cached'
      ? [{ icon: 'clock' as const, text: `Cached · read only${decision.cachedAt ? ` · ${ago(decision.cachedAt)}` : ''}`, tone: 'warn' as const }]
      : [{ icon: 'wifi' as const, text: decision ? 'Not connected' : 'Connecting…', tone: decision ? 'crit' as const : undefined }]
  const modal = top?.name === 'pair' || top?.name === 'login'

  return <SummaryCtx.Provider value={{ r: sum.r, err: sum.err, reload: sum.reload }}>
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <StatusBar style={scheme === 'dark' ? 'light' : 'dark'} />
      <TopBar onBack={top ? back : undefined} onAccount={() => go('account')} accountActive={!top && tab === 'account'} />
      {pairing && !modal && <StatusStrip items={statusItems} simulated={d?.contains_simulated_data} />}
      <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
        {top?.name === 'unit' ? <UnitScreen key={top.id} id={top.id} go={go} />
          : top?.name === 'pair' ? <PairScreen done={back} />
          : top?.name === 'login' ? <LoginScreen done={back} />
          : tab === 'overview' ? <Overview go={go} />
          : tab === 'alerts' ? <AlertsScreen go={go} />
          : <AccountScreen go={go} />}
      </KeyboardAvoidingView>
      {!modal && <TabBar tab={top ? (top.name === 'unit' ? 'overview' : null) : tab} onTab={t => { setStack([]); setTab(t) }} alertCount={d ? d.active_incidents.total ?? d.active_incidents.critical + d.active_incidents.warning : 0} />}
    </View>
  </SummaryCtx.Provider>
}

export default function App() {
  return <SafeAreaProvider><ThemeProvider><AppProvider><Shell /></AppProvider></ThemeProvider></SafeAreaProvider>
}
