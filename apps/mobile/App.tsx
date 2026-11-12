import { StatusBar } from 'expo-status-bar'
import { useState } from 'react'
import { SafeAreaView } from 'react-native'
import { AppProvider } from './src/ui/AppState'
import { Home, LoginScreen, PairScreen, SettingsScreen, UnitScreen } from './src/ui/screens'

type Route = { name: 'home' } | { name: 'unit'; id: string } | { name: 'pair' } | { name: 'login' } | { name: 'settings' }

export default function App() {
  const [route, setRoute] = useState<Route>({ name: 'home' })
  const home = () => setRoute({ name: 'home' })
  return (
    <AppProvider>
      <SafeAreaView style={{ flex: 1 }}>
        <StatusBar style="dark" />
        {route.name === 'home' && <Home go={(to, arg) => setRoute(to === 'unit' ? { name: 'unit', id: arg! } : ({ name: to } as Route))} />}
        {route.name === 'unit' && <UnitScreen id={route.id} back={home} />}
        {route.name === 'pair' && <PairScreen back={home} />}
        {route.name === 'login' && <LoginScreen back={home} />}
        {route.name === 'settings' && <SettingsScreen back={home} pair={() => setRoute({ name: 'pair' })} />}
      </SafeAreaView>
    </AppProvider>
  )
}
