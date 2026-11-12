import { describe, expect, it } from 'vitest'
import * as ed from '@noble/ed25519'
import { readFileSync } from 'node:fs'
import { acceptableHint, b64urlEncode, newChallenge, parsePairingPayload, PairingError, verifyIdentity } from './identity'
import { decide, probeLocal } from './connection'
import { MemoryStore, NamespacedCache, TokenStore } from './store'
import { ApiFailure, freshnessLabel, SourceSession } from './session'
import type { Env, Mode, Pairing } from './types'
import { sha512 } from '@noble/hashes/sha2.js'
ed.hashes.sha512 = sha512

// ---- a fake Pi with a real Ed25519 site key, speaking the real identity scheme
const MSG = 'aerolab-identity-v1\n'
function makePi(siteId: string, seedByte: number) {
  const sk = new Uint8Array(32).fill(seedByte)
  const pk = ed.getPublicKey(sk)
  return {
    siteId, pk: b64urlEncode(pk),
    answer(challenge: string) {
      const sig = ed.sign(new TextEncoder().encode(MSG + siteId + '\n' + challenge), sk)
      return { site_id: siteId, site_name: 'x', public_key: b64urlEncode(pk), signature: b64urlEncode(sig) }
    },
  }
}
type Handler = (url: URL, init?: RequestInit) => Response | Promise<Response> | 'hang' | 'refuse'
function fakeFetch(routes: Record<string, Handler>): typeof fetch {
  return (async (input: string | URL | Request, init?: RequestInit) => {
    const u = new URL(typeof input === 'string' ? input : input.toString())
    const h = routes[u.origin]
    if (!h) throw new TypeError('network unreachable')
    const r = await h(u, init)
    if (r === 'refuse') throw new TypeError('connection refused')
    if (r === 'hang') return new Promise<Response>((_, rej) => init?.signal?.addEventListener('abort', () => rej(new DOMException('aborted', 'AbortError'))))
    return r
  }) as typeof fetch
}
const json = (o: unknown, status = 200) => new Response(JSON.stringify(o), { status, headers: { 'Content-Type': 'application/json' } })
const rnd = () => { let n = 1; return (len: number) => Uint8Array.from({ length: len }, () => (n = (n * 31 + 7) & 255)) }

const PI = makePi('site-pilot-01', 7)
const pairing = (over: Partial<Pairing> = {}): Pairing => ({ siteId: 'site-pilot-01', name: 'Pilot', publicKey: PI.pk, localHints: ['http://192.168.1.50'], cloudUrl: 'https://cloud.example.org', pairedAt: 1, ...over })
const piRoutes = (pi = PI): Record<string, Handler> => ({ 'http://192.168.1.50': u => (u.pathname === '/api/v1/identity' ? json(pi.answer(u.searchParams.get('challenge')!)) : json({}, 404)) })
const env = (routes: Record<string, Handler>, timeout = 80): Env => ({ fetch: fakeFetch(routes), now: () => 1_000_000, random: rnd(), probeTimeoutMs: timeout })

// ---------------------------------------------------------------------------------------------------

describe('identity (cross-language contract with the Go Pi)', () => {
  it('verifies the vector produced by the real Go implementation', () => {
    const v = JSON.parse(readFileSync(new URL('../../../../contracts/examples/identity.vector.json', import.meta.url), 'utf8'))
    expect(verifyIdentity({ siteId: v.site_id, publicKey: v.public_key }, v.challenge, { site_id: v.site_id, public_key: v.public_key, signature: v.signature })).toBe(true)
  })
  it('rejects every kind of wrong answer', () => {
    const v = JSON.parse(readFileSync(new URL('../../../../contracts/examples/identity.vector.json', import.meta.url), 'utf8'))
    const p = { siteId: v.site_id, publicKey: v.public_key }, ok = { site_id: v.site_id, public_key: v.public_key, signature: v.signature }
    expect(verifyIdentity(p, v.challenge + 'x', ok)).toBe(false)                       // replay against another challenge
    expect(verifyIdentity({ ...p, siteId: 'site-other-99' }, v.challenge, { ...ok, site_id: 'site-other-99' })).toBe(false) // wrong site
    expect(verifyIdentity(p, v.challenge, { ...ok, public_key: makePi('x', 9).pk })).toBe(false) // different key than pinned
    expect(verifyIdentity(p, v.challenge, { ...ok, signature: 'AAAA' })).toBe(false)
    expect(verifyIdentity(p, v.challenge, { ...ok, signature: '!!!' })).toBe(false)
  })
})

describe('pairing QR payload (untrusted input)', () => {
  const good = { v: 1, site: 'site-pilot-01', name: 'Pilot', pk: PI.pk, hints: ['http://192.168.1.50:8080'], code: 'ABCD-2345' }
  it('accepts a valid payload and normalises it', () => {
    const p = parsePairingPayload(JSON.stringify({ ...good, code: 'abcd-2345' }))
    expect(p.code).toBe('ABCD-2345'); expect(p.hints).toEqual(['http://192.168.1.50:8080'])
  })
  it.each([
    ['not json', 'hello'], ['wrong version', JSON.stringify({ ...good, v: 2 })], ['bad site id', JSON.stringify({ ...good, site: 'Site/../x' })],
    ['short key', JSON.stringify({ ...good, pk: 'AAAA' })], ['bad code', JSON.stringify({ ...good, code: 'x' })],
    ['no usable hints', JSON.stringify({ ...good, hints: ['javascript:alert(1)', 'file:///etc/passwd', 'ftp://x'] })],
    ['public http host', JSON.stringify({ ...good, hints: ['http://evil.example.com'] })], ['oversize', 'x'.repeat(3000)],
  ])('rejects %s', (_n, raw) => { expect(() => parsePairingPayload(raw)).toThrow(PairingError) })
  it('allows plain http only for private/lab hosts and strips anything but the origin', () => {
    expect(acceptableHint('https://pi.example.org/x?y#z')).toBeNull() // query/fragment refused
    expect(acceptableHint('https://pi.example.org')).toEqual({ url: 'https://pi.example.org', insecureLab: false })
    expect(acceptableHint('http://10.1.2.3')?.insecureLab).toBe(true)
    expect(acceptableHint('http://172.20.0.5')?.insecureLab).toBe(true)
    expect(acceptableHint('http://172.40.0.5')).toBeNull()
    expect(acceptableHint('http://aerolab.local')?.insecureLab).toBe(true)
    expect(acceptableHint('http://user:pw@192.168.1.1')).toBeNull()
  })
})

describe('probeLocal', () => {
  it('accepts the real Pi', async () => { expect(await probeLocal(env(piRoutes()), pairing())).toMatchObject({ ok: true, baseUrl: 'http://192.168.1.50', insecureLab: true }) })
  it('refuses an impostor that answers with ITS OWN key, even for the right site id', async () => {
    const impostor = makePi('site-pilot-01', 99)
    expect(await probeLocal(env(piRoutes(impostor)), pairing())).toEqual({ ok: false, reason: 'LOCAL_IDENTITY_MISMATCH' })
  })
  it('refuses a replayed fixed answer (it must answer OUR challenge)', async () => {
    const fixed = PI.answer(b64urlEncode(Uint8Array.from({ length: 16 }, (_, i) => 200 - i))) // an old, captured answer
    const r = env({ 'http://192.168.1.50': () => json(fixed) })
    expect((await probeLocal(r, pairing())).ok).toBe(false)
  })
  it('times out quickly and tries the next hint', async () => {
    const e = env({ 'http://192.168.1.99': () => 'hang', ...piRoutes() }, 50)
    const t0 = Date.now()
    expect(await probeLocal(e, pairing({ localHints: ['http://192.168.1.99', 'http://192.168.1.50'] }))).toMatchObject({ ok: true, baseUrl: 'http://192.168.1.50' })
    expect(Date.now() - t0).toBeLessThan(1000)
  })
  it('reports unreachable when nothing answers', async () => { expect(await probeLocal(env({}), pairing())).toEqual({ ok: false, reason: 'LOCAL_UNREACHABLE' }) })
})

describe('Connection Manager decision table', () => {
  const run = (o: { mode: Mode; localUp?: boolean; impostor?: boolean; localSession?: boolean; cloudSession?: boolean; cloudUp?: boolean; cache?: { source: 'local' | 'cloud'; at: number } | null; cloudUrl?: boolean }) =>
    decide({
      env: env(o.localUp ? piRoutes(o.impostor ? makePi('site-pilot-01', 99) : PI) : {}), mode: o.mode, pairing: pairing(o.cloudUrl === false ? { cloudUrl: undefined } : {}),
      hasSession: async s => (s === 'local' ? !!o.localSession : !!o.cloudSession), cloudReachable: async () => !!o.cloudUp, cachedFrom: async () => o.cache ?? null,
    })

  it('auto: verified local + local session => LOCAL (and the cloud is not even needed)', async () => {
    expect(await run({ mode: 'auto', localUp: true, localSession: true, cloudSession: true, cloudUp: true })).toMatchObject({ kind: 'live', source: 'local', canControl: true })
  })
  it('auto: not on the site Wi-Fi => CLOUD when a cloud session exists', async () => {
    expect(await run({ mode: 'auto', localUp: false, cloudSession: true, cloudUp: true })).toMatchObject({ kind: 'live', source: 'cloud', baseUrl: 'https://cloud.example.org' })
  })
  it('auto: local reachable but not signed in => cloud if possible, otherwise ask for a LOCAL login', async () => {
    expect(await run({ mode: 'auto', localUp: true, cloudSession: true, cloudUp: true })).toMatchObject({ kind: 'live', source: 'cloud' })
    expect(await run({ mode: 'auto', localUp: true })).toMatchObject({ kind: 'unavailable', reason: 'LOCAL_LOGIN_REQUIRED', needsLocalLogin: true, canControl: false })
  })
  it('auto: nothing reachable => explicit READ-ONLY cache with its age, never control', async () => {
    const d = await run({ mode: 'auto', cache: { source: 'local', at: 123 } })
    expect(d).toMatchObject({ kind: 'cached', canControl: false, cachedFrom: 'local', cachedAt: 123 })
  })
  it('auto: nothing reachable and no cache => unavailable', async () => { expect(await run({ mode: 'auto' })).toMatchObject({ kind: 'unavailable', reason: 'NO_SOURCE_AVAILABLE', canControl: false }) })
  it('an impostor Pi is never used, and the cloud fallback is still allowed only with a cloud session', async () => {
    expect(await run({ mode: 'auto', localUp: true, impostor: true, localSession: true })).toMatchObject({ kind: 'unavailable', reason: 'LOCAL_IDENTITY_MISMATCH' })
    expect(await run({ mode: 'auto', localUp: true, impostor: true, localSession: true, cloudSession: true, cloudUp: true })).toMatchObject({ kind: 'live', source: 'cloud' })
  })
  it('local-only NEVER falls back to the cloud (no admin action silently sent to the wrong place)', async () => {
    const d = await run({ mode: 'local', localUp: false, cloudSession: true, cloudUp: true, cache: { source: 'cloud', at: 5 } })
    expect(d.kind).not.toBe('live'); expect(d).toMatchObject({ kind: 'unavailable', canControl: false }) // a CLOUD cache is not offered either
    expect(await run({ mode: 'local', localUp: true, localSession: true })).toMatchObject({ kind: 'live', source: 'local' })
  })
  it('cloud-only never touches the local network', async () => {
    let probed = false
    const e = env({ 'http://192.168.1.50': () => { probed = true; return json({}) } })
    const d = await decide({ env: e, mode: 'cloud', pairing: pairing(), hasSession: async s => s === 'cloud', cloudReachable: async () => true, cachedFrom: async () => null })
    expect(d).toMatchObject({ kind: 'live', source: 'cloud' }); expect(probed).toBe(false)
    expect(await run({ mode: 'cloud', localUp: true, localSession: true })).toMatchObject({ kind: 'unavailable', reason: 'CLOUD_LOGIN_REQUIRED' })
    expect(await run({ mode: 'cloud', cloudSession: true, cloudUp: false, cache: { source: 'cloud', at: 9 } })).toMatchObject({ kind: 'cached', cachedFrom: 'cloud', reason: 'CLOUD_UNREACHABLE' })
  })
})

describe('source switching and cache isolation', () => {
  const setup = () => {
    const secrets = new MemoryStore(), cacheKv = new MemoryStore()
    const cache = new NamespacedCache(cacheKv, () => 1000)
    const calls: { url: string; auth: string | null }[] = []
    let delay = 0
    const f = (async (url: string, init?: RequestInit) => {
      const auth = new Headers(init?.headers).get('Authorization'); calls.push({ url, auth })
      if (delay) await new Promise(r => setTimeout(r, delay))
      if (init?.signal?.aborted) throw new DOMException('aborted', 'AbortError')
      return json({ from: url })
    }) as unknown as typeof fetch
    const s = new SourceSession(secrets, cache, f, () => 2000)
    return { s, secrets, cache, calls, cacheKv, setDelay: (d: number) => (delay = d) }
  }

  it('uses a different token per source and never mixes them', async () => {
    const { s, calls } = setup()
    await s.tokens.set('site-a-0001', 'local', 'LOCAL-TOKEN'); await s.tokens.set('site-a-0001', 'cloud', 'CLOUD-TOKEN')
    s.switchTo('site-a-0001', 'local', 'http://192.168.1.50', 'u1'); await s.get('/api/v1/x')
    s.switchTo('site-a-0001', 'cloud', 'https://cloud.example.org', 'u1'); await s.get('/api/v1/x')
    expect(calls.map(c => c.auth)).toEqual(['Bearer LOCAL-TOKEN', 'Bearer CLOUD-TOKEN'])
  })
  it('local cache is never served under the cloud namespace (or to another user)', async () => {
    const { s } = setup()
    await s.tokens.set('site-a-0001', 'local', 't')
    s.switchTo('site-a-0001', 'local', 'http://192.168.1.50', 'u1'); await s.get('/api/v1/units')
    expect(await s.cached('/api/v1/units')).toMatchObject({ source: 'local', fromCache: true })
    s.switchTo('site-a-0001', 'cloud', 'https://cloud.example.org', 'u1'); expect(await s.cached('/api/v1/units')).toBeNull()
    s.switchTo('site-a-0001', 'local', 'http://192.168.1.50', 'u2'); expect(await s.cached('/api/v1/units')).toBeNull()
    s.switchTo('site-b-0001', 'local', 'http://192.168.1.50', 'u1'); expect(await s.cached('/api/v1/units')).toBeNull()
  })
  it('a corrupted/relabelled cache record is discarded, not shown', async () => {
    const { s, cacheKv } = setup()
    await s.tokens.set('site-a-0001', 'local', 't'); s.switchTo('site-a-0001', 'local', 'http://192.168.1.50', 'u1'); await s.get('/api/v1/units')
    const key = (await cacheKv.keys())![0]; const v = JSON.parse((await cacheKv.get(key))!); v.source = 'cloud'; await cacheKv.set(key, JSON.stringify(v))
    expect(await s.cached('/api/v1/units')).toBeNull()
  })
  it('a switch aborts in-flight requests, and a late response can never land in the new source', async () => {
    const { s, setDelay } = setup()
    await s.tokens.set('site-a-0001', 'local', 'L'); await s.tokens.set('site-a-0001', 'cloud', 'C')
    s.switchTo('site-a-0001', 'local', 'http://192.168.1.50', 'u1'); setDelay(60)
    const pending = s.get('/api/v1/units').catch(e => e)
    await new Promise(r => setTimeout(r, 10))
    let dropped = 0; s.onSwitch(() => dropped++)
    s.switchTo('site-a-0001', 'cloud', 'https://cloud.example.org', 'u1')
    const e = await pending
    expect(e).toBeInstanceOf(ApiFailure); expect((e as ApiFailure).code).toMatch(/SUPERSEDED|NETWORK/)
    expect(dropped).toBe(1)
    expect(await s.cached('/api/v1/units')).toBeNull() // nothing from the old source was stored under the new one
  })
  it('refuses to send any write unless the session is LIVE (cached data is read-only)', async () => {
    const { s, calls } = setup()
    await s.tokens.set('site-a-0001', 'local', 't'); s.switchTo('site-a-0001', 'local', 'http://192.168.1.50', 'u1')
    await expect(s.post('/api/v1/incidents/x/acknowledge', {}, 'k1', false)).rejects.toMatchObject({ code: 'READ_ONLY' })
    expect(calls).toHaveLength(0)
    await s.post('/api/v1/incidents/x/acknowledge', {}, 'k1', true); expect(calls).toHaveLength(1)
  })
  it('a 401 clears that source token only', async () => {
    const secrets = new MemoryStore(); const cache = new NamespacedCache(new MemoryStore(), () => 1)
    const s = new SourceSession(secrets, cache, (async () => new Response('', { status: 401 })) as unknown as typeof fetch, () => 1)
    const t = new TokenStore(secrets); await t.set('site-a-0001', 'local', 'L'); await t.set('site-a-0001', 'cloud', 'C')
    s.switchTo('site-a-0001', 'local', 'http://192.168.1.50', 'u1')
    await expect(s.get('/x')).rejects.toMatchObject({ status: 401 })
    expect(await t.get('site-a-0001', 'local')).toBeNull(); expect(await t.get('site-a-0001', 'cloud')).toBe('C')
  })
  it('clearNamespace removes only that namespace', async () => {
    const kv = new MemoryStore(); const c = new NamespacedCache(kv, () => 1)
    await c.put('s', 'local', 'u', 'a', 1); await c.put('s', 'cloud', 'u', 'a', 2)
    await c.clearNamespace('s', 'local', 'u')
    expect(await c.get('s', 'local', 'u', 'a')).toBeNull(); expect((await c.get('s', 'cloud', 'u', 'a'))?.data).toBe(2)
  })
})

describe('freshness wording', () => {
  it('says cached/read-only explicitly, with the age and source', () => {
    expect(freshnessLabel({ fetchedAt: 0, fromCache: true, source: 'local' }, 7200_000)).toBe('Cached as of 2 h ago (local) – read only')
    expect(freshnessLabel({ fetchedAt: 1000, fromCache: false, source: 'cloud' }, 1000)).toBe('Cloud · updated just now')
    expect(freshnessLabel({ fetchedAt: 0, fromCache: false, source: 'local' }, 30_000)).toBe('Site · updated 30 s ago')
  })
})
