import type { KeyValueStore, Source } from './types'

export class MemoryStore implements KeyValueStore {
  m = new Map<string, string>()
  async get(k: string) { return this.m.get(k) ?? null }
  async set(k: string, v: string) { this.m.set(k, v) }
  async remove(k: string) { this.m.delete(k) }
  async keys() { return [...this.m.keys()] }
}

/** Session tokens are stored per (site, source): a local token is never presented to the cloud or vice versa. */
export class TokenStore {
  constructor(private kv: KeyValueStore) {}
  private k(site: string, source: Source) { return `tok|${site}|${source}` }
  get(site: string, source: Source) { return this.kv.get(this.k(site, source)) }
  set(site: string, source: Source, token: string) { return this.kv.set(this.k(site, source), token) }
  clear(site: string, source: Source) { return this.kv.remove(this.k(site, source)) }
}

export interface Cached<T> { data: T; storedAt: number; source: Source; siteId: string; userId: string }

/**
 * Read-model cache. The namespace is site + source + user + resource, so local values can never be
 * shown as cloud values (or to another user), and a source switch can never mix caches.
 */
export class NamespacedCache {
  constructor(private kv: KeyValueStore, private now: () => number) {}
  private k(siteId: string, source: Source, userId: string, resource: string) { return `c|${siteId}|${source}|${userId}|${resource}` }
  async put<T>(siteId: string, source: Source, userId: string, resource: string, data: T) {
    const v: Cached<T> = { data, storedAt: this.now(), source, siteId, userId }
    await this.kv.set(this.k(siteId, source, userId, resource), JSON.stringify(v))
  }
  async get<T>(siteId: string, source: Source, userId: string, resource: string): Promise<Cached<T> | null> {
    const raw = await this.kv.get(this.k(siteId, source, userId, resource))
    if (!raw) return null
    try {
      const v = JSON.parse(raw) as Cached<T>
      // belt and braces: a record whose embedded namespace disagrees with its key is discarded
      return v.siteId === siteId && v.source === source && v.userId === userId ? v : null
    } catch { return null }
  }
  async clearNamespace(siteId: string, source: Source, userId: string) {
    const prefix = `c|${siteId}|${source}|${userId}|`
    for (const key of (await this.kv.keys?.()) ?? []) if (key.startsWith(prefix)) await this.kv.remove(key)
  }
}
