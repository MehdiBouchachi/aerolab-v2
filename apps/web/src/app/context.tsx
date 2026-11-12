import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { api, hasToken, openEvents, setToken, setUnauthorizedHandler, type SseEvent, type StreamState } from '../api/client'
import { IS_CLOUD, P } from '../api/paths'
import type { Session } from '../api/types'

type Listener = (e: SseEvent) => void
interface Ctx {
  session: Session | null; stream: StreamState; lastEventAt: number | null
  signIn: (u: string, p: string) => Promise<void>; signOut: () => Promise<void>; selectSite: (id: string) => Promise<void>
  subscribe: (l: Listener) => () => void
  can: (cap: string) => boolean
}
const C = createContext<Ctx | null>(null)
function readSite(): string | undefined { try { return sessionStorage.getItem('aerolab_site') ?? undefined } catch { return undefined } }
export const useApp = () => { const c = useContext(C); if (!c) throw new Error('no AppProvider'); return c }

export function AppProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session | null>(null)
  const [booting, setBooting] = useState(hasToken())
  const [stream, setStream] = useState<StreamState>('connecting')
  const [lastEventAt, setLast] = useState<number | null>(null)
  const listeners = useRef(new Set<Listener>())

  const drop = useCallback(() => { setToken(null); setSession(null) }, [])
  useEffect(() => { setUnauthorizedHandler(drop) }, [drop])
  useEffect(() => {
    if (!hasToken()) return
    api<Session>('GET', P.session(readSite())).then(setSession).catch(drop).finally(() => setBooting(false))
  }, [drop])

  // one live stream per signed-in session; consumers refetch on events
  const siteId = session?.site.id
  useEffect(() => {
    if (!siteId) return
    if (IS_CLOUD) { setStream('polling'); return } // the cloud has no live stream: views poll and show their age
    return openEvents(siteId, e => { setLast(Date.now()); listeners.current.forEach(l => l(e)) }, setStream)
  }, [siteId])

  const value = useMemo<Ctx>(() => ({
    session, stream, lastEventAt,
    async signIn(username, password) {
      const r = await api<{ token: string }>('POST', P.login, IS_CLOUD ? { email: username, password } : { username, password })
      setToken(r.token)
      setSession(await api<Session>('GET', P.session(readSite())))
    },
    async signOut() { try { await api('POST', P.logout) } finally { drop() } },
    async selectSite(id) { try { sessionStorage.setItem('aerolab_site', id) } catch { /* ignore */ } setSession(await api<Session>('GET', P.session(id))) },
    subscribe(l) { listeners.current.add(l); return () => { listeners.current.delete(l) } },
    can: cap => !!session?.capabilities.includes(cap),
  }), [session, stream, lastEventAt, drop])

  if (booting) return <p className="boot">Loading…</p>
  return <C.Provider value={value}>{children}</C.Provider>
}
