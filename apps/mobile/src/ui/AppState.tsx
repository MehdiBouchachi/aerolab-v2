import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { decide } from '../core/connection'
import { NamespacedCache, TokenStore } from '../core/store'
import { SourceSession } from '../core/session'
import type { Decision, Env, Mode, Pairing } from '../core/types'
import { cacheStore, random, secureStore } from '../native'

interface Ctx {
  pairings: Pairing[]
  pairing: Pairing | null
  mode: Mode
  decision: Decision | null
  session: SourceSession
  userId: string | null
  busy: boolean
  refresh: () => Promise<void>
  addPairing: (p: Pairing) => Promise<void>
  removePairing: (siteId: string) => Promise<void>
  selectSite: (siteId: string) => Promise<void>
  setMode: (m: Mode) => Promise<void>
  setCloudUrl: (url: string) => Promise<void>
  signedIn: (source: 'local' | 'cloud', username: string, token: string) => Promise<void>
  env: Env
}
const C = createContext<Ctx | null>(null)
export const useApp = () => { const c = useContext(C); if (!c) throw new Error('no provider'); return c }

const PAIRINGS = 'pairings', SELECTED = 'selected_site', MODE = 'mode', USER = 'user|'

export function AppProvider({ children }: { children: ReactNode }) {
  const env = useMemo<Env>(() => ({ fetch: (...a) => fetch(...a), now: () => Date.now(), random, probeTimeoutMs: 2500 }), [])
  const cache = useMemo(() => new NamespacedCache(cacheStore, () => Date.now()), [])
  const session = useMemo(() => new SourceSession(secureStore, cache, (...a) => fetch(...a), () => Date.now()), [cache])
  const tokens = useMemo(() => new TokenStore(secureStore), [])
  const [pairings, setPairings] = useState<Pairing[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [mode, setModeState] = useState<Mode>('auto')
  const [decision, setDecision] = useState<Decision | null>(null)
  const [userId, setUserId] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const pairing = pairings.find(p => p.siteId === selected) ?? pairings[0] ?? null
  const running = useRef(false)

  useEffect(() => { void (async () => {
    setPairings(JSON.parse((await cacheStore.get(PAIRINGS)) ?? '[]'))
    setSelected(await cacheStore.get(SELECTED))
    setModeState(((await cacheStore.get(MODE)) as Mode | null) ?? 'auto')
  })() }, [])

  const refresh = useCallback(async () => {
    if (!pairing || running.current) return
    running.current = true; setBusy(true)
    try {
      const lastUser = async (src: 'local' | 'cloud') => (await cacheStore.get(USER + pairing.siteId + '|' + src)) ?? ''
      const d = await decide({
        env, pairing, mode,
        hasSession: async s => !!(await tokens.get(pairing.siteId, s)),
        cloudReachable: async () => { try { const r = await fetch(`${pairing.cloudUrl}/healthz`); return r.ok } catch { return false } },
        cachedFrom: async () => {
          for (const src of ['local', 'cloud'] as const) {
            const c = await cache.get<unknown>(pairing.siteId, src, await lastUser(src), `/api/v1/sites/${pairing.siteId}/summary`)
            if (c && (mode === 'auto' || mode === src)) return { source: src, at: c.storedAt }
          }
          return null
        },
      })
      setDecision(d)
      if (d.kind === 'live') { const u = await lastUser(d.source); setUserId(u); session.switchTo(pairing.siteId, d.source, d.baseUrl, u) }
      else if (d.kind === 'cached' && d.cachedFrom) { const u = await lastUser(d.cachedFrom); setUserId(u); session.switchTo(pairing.siteId, d.cachedFrom, '', u) }
    } finally { running.current = false; setBusy(false) }
  }, [pairing, mode, env, tokens, cache, session])

  useEffect(() => { void refresh(); const t = setInterval(() => void refresh(), 15000); return () => clearInterval(t) }, [refresh])

  const value: Ctx = {
    pairings, pairing, mode, decision, session, userId, busy, refresh, env,
    async addPairing(p) { const next = [...pairings.filter(x => x.siteId !== p.siteId), p]; setPairings(next); await cacheStore.set(PAIRINGS, JSON.stringify(next)); setSelected(p.siteId); await cacheStore.set(SELECTED, p.siteId) },
    async removePairing(id) {
      const next = pairings.filter(x => x.siteId !== id); setPairings(next); await cacheStore.set(PAIRINGS, JSON.stringify(next))
      for (const s of ['local', 'cloud'] as const) { await tokens.clear(id, s); await cache.clearNamespace(id, s, await cacheStore.get(USER + id + '|' + s) ?? '') }
    },
    async selectSite(id) { setSelected(id); await cacheStore.set(SELECTED, id) },
    async setMode(m) { setModeState(m); await cacheStore.set(MODE, m) },
    async setCloudUrl(url) { if (!pairing) return; const next = pairings.map(p => (p.siteId === pairing.siteId ? { ...p, cloudUrl: url || undefined } : p)); setPairings(next); await cacheStore.set(PAIRINGS, JSON.stringify(next)) },
    async signedIn(source, username, token) {
      if (!pairing) return
      await tokens.set(pairing.siteId, source, token); await cacheStore.set(USER + pairing.siteId + '|' + source, username)
      await refresh()
    },
  }
  return <C.Provider value={value}>{children}</C.Provider>
}
