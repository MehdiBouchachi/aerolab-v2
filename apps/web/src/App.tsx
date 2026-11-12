import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AppProvider, useApp } from './app/context'
import { Shell } from './app/Shell'
import { Login } from './pages/Login'
import { Overview } from './pages/Overview'
import { UnitPage } from './pages/UnitPage'
import { Incidents } from './pages/Incidents'
import { Admin } from './pages/Admin'

function Gate() {
  const { session } = useApp()
  const { signOut } = useApp()
  if (!session) return <Login />
  if (!session.site) return <div className="login"><div className="panel"><h1>No sites</h1><p>This account is not a member of any site yet. Ask a site administrator to add you.</p><button onClick={() => void signOut()}>Sign out</button></div></div>
  return (
    <Routes>
      <Route element={<Shell />}>
        <Route index element={<Overview />} />
        <Route path="units/:unitId" element={<UnitPage />} />
        <Route path="incidents" element={<Incidents />} />
        <Route path="admin" element={<Admin />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

export default function App() {
  return <AppProvider><BrowserRouter><Gate /></BrowserRouter></AppProvider>
}
