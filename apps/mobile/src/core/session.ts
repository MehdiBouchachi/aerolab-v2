import type { NamespacedCache } from './store'
import type { Source, SecretStore } from './types'
import { TokenStore } from './store'

export class ApiFailure extends Error {
  constructor(public status: number, public code: string, message: string) { super(message) }
}

export interface Reading<T> { data: T; source: Source; fetchedAt: number; fromCache: boolean }

/**
 * One authenticated data source at a time. A switch (local<->cloud, or another site/user) is a NEW
 * session: in-flight requests of the old one are aborted, its listeners dropped, and any response that
 * still arrives late is discarded by the generation check - it can never land in the new source's UI.
 */
export class SourceSession {
  private gen = 0
  private inflight = new Set<AbortController>()
  private listeners = new Set<() => void>()
  tokens: TokenStore
  siteId = ''
  source: Source = 'local'
  baseUrl = ''
  userId = ''

  constructor(secrets: SecretStore, private cache: NamespacedCache, private f: typeof fetch, private now: () => number) { this.tokens = new TokenStore(secrets) }

  get key() { return `${this.siteId}|${this.source}|${this.userId}` }
  onSwitch(l: () => void) { this.listeners.add(l); return () => this.listeners.delete(l) }

  /** Switch source/site/user. Returns the new generation. */
  switchTo(siteId: string, source: Source, baseUrl: string, userId: string): number {
    const changed = siteId !== this.siteId || source !== this.source || userId !== this.userId || baseUrl !== this.baseUrl
    if (changed) {
      for (const c of this.inflight) c.abort()
      this.inflight.clear()
      this.listeners.forEach(l => l())
      this.listeners.clear()
      this.gen++
    }
    this.siteId = siteId; this.source = source; this.baseUrl = baseUrl; this.userId = userId
    return this.gen
  }

  /** GET JSON from THIS source only. On success it is cached under this source's namespace. */
  async get<T>(path: string, timeoutMs = 8000): Promise<Reading<T>> {
    const gen = this.gen, source = this.source, site = this.siteId, user = this.userId, base = this.baseUrl
    const token = await this.tokens.get(site, source)
    if (!token) throw new ApiFailure(401, 'UNAUTHORIZED', 'Sign in required')
    const ctl = new AbortController()
    this.inflight.add(ctl)
    const timer = setTimeout(() => ctl.abort(), timeoutMs)
    try {
      const res = await this.f(base + path, { signal: ctl.signal, headers: { Authorization: `Bearer ${token}`, Accept: 'application/json' } })
      if (gen !== this.gen) throw new ApiFailure(0, 'SUPERSEDED', 'Source changed while loading')
      if (res.status === 401) { await this.tokens.clear(site, source); throw new ApiFailure(401, 'UNAUTHORIZED', 'Session expired') }
      if (!res.ok) throw new ApiFailure(res.status, 'ERROR', `Request failed (${res.status})`)
      const data = (await res.json()) as T
      if (gen !== this.gen) throw new ApiFailure(0, 'SUPERSEDED', 'Source changed while loading')
      await this.cache.put(site, source, user, path, data)
      return { data, source, fetchedAt: this.now(), fromCache: false }
    } catch (e) {
      if (e instanceof ApiFailure) throw e
      throw new ApiFailure(0, 'NETWORK', 'Cannot reach this source')
    } finally { clearTimeout(timer); this.inflight.delete(ctl) }
  }

  /** Read-only cached copy for THIS source/user namespace (explicitly marked fromCache). */
  async cached<T>(path: string): Promise<Reading<T> | null> {
    const c = await this.cache.get<T>(this.siteId, this.source, this.userId, path)
    return c ? { data: c.data, source: c.source, fetchedAt: c.storedAt, fromCache: true } : null
  }

  /** The ONLY way to write. Refuses unless this is a live session, and uses an idempotency key per user action. */
  async post<T>(path: string, body: unknown, idempotencyKey: string, live: boolean): Promise<T> {
    if (!live) throw new ApiFailure(0, 'READ_ONLY', 'Cached data is read-only; connect to the site or cloud to make changes')
    const token = await this.tokens.get(this.siteId, this.source)
    if (!token) throw new ApiFailure(401, 'UNAUTHORIZED', 'Sign in required')
    const res = await this.f(this.baseUrl + path, { method: 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey }, body: JSON.stringify(body) })
    if (!res.ok) throw new ApiFailure(res.status, 'ERROR', `Request failed (${res.status})`)
    return (await res.json()) as T
  }
}

/** Plain-language freshness for badges: live data shows its age, cached data says so explicitly. */
export function freshnessLabel(r: { fetchedAt: number; fromCache: boolean; source: Source }, now: number): string {
  const s = Math.max(0, Math.round((now - r.fetchedAt) / 1000))
  const age = s < 5 ? 'just now' : s < 90 ? `${s} s ago` : s < 5400 ? `${Math.round(s / 60)} min ago` : s < 172800 ? `${Math.round(s / 3600)} h ago` : `${Math.round(s / 86400)} days ago`
  return r.fromCache ? `Cached as of ${age} (${r.source}) – read only` : `${r.source === 'local' ? 'Site' : 'Cloud'} · updated ${age}`
}
