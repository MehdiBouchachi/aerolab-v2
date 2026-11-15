import { lazy, Suspense } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AppProvider, useApp } from './app/context'
import { Shell } from './app/Shell'
import { Login } from './pages/Login'
import { Overview } from './pages/Overview'
import { Incidents } from './pages/Incidents'
import { ThemeProvider } from './app/theme'

// Loaded on demand: the overview (what most visits open) no longer downloads charts, the programme
// editor, the QR-code library or the admin pages.
const UnitPage = lazy(() => import('./pages/UnitPage').then(m => ({ default: m.UnitPage })))
const Admin = lazy(() => import('./pages/Admin').then(m => ({ default: m.Admin })))
const Page = ({ children }: { children: React.ReactNode }) => <Suspense fallback={<p className="muted" role="status">Loading…</p>}>{children}</Suspense>

function Gate() {
  const { session } = useApp()
  const { signOut } = useApp()
  if (!session) return <Login />
  if (!session.site) return <div className="login"><div className="panel"><h1>No sites</h1><p>This account is not a member of any site yet. Ask a site administrator to add you.</p><button onClick={() => void signOut()}>Sign out</button></div></div>
  return (
    <Routes>
      <Route element={<Shell />}>
        <Route index element={<Overview />} />
        <Route path="units/:unitId" element={<Page><UnitPage /></Page>} />
        <Route path="incidents" element={<Incidents />} />
        <Route path="admin" element={<Page><Admin /></Page>} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

export default function App() {
  return <ThemeProvider><AppProvider><BrowserRouter><Gate /></BrowserRouter></AppProvider></ThemeProvider>
}
