// Opt-in: talks to a REAL edge (Go) over HTTP. Run with the Go hold-open stack:
//   AEROLAB_HOLD=60 go test -run TestHoldStack ./tests/integration/   (prints the edge port + admin password)
//   AEROLAB_E2E_EDGE=http://127.0.0.1:PORT AEROLAB_E2E_PW=... pnpm test src/e2e
import { describe, expect, it } from 'vitest'
import { decide, probeLocal } from '../core/connection'
import { parsePairingPayload, verifyIdentity, newChallenge } from '../core/identity'
import { SourceSession } from '../core/session'
import { MemoryStore, NamespacedCache } from '../core/store'
import type { Env, Pairing } from '../core/types'
import { randomBytes } from 'node:crypto'

const EDGE = process.env.AEROLAB_E2E_EDGE, PW = process.env.AEROLAB_E2E_PW
const d = EDGE && PW ? describe : describe.skip
const env: Env = { fetch, now: () => Date.now(), random: n => new Uint8Array(randomBytes(n)), probeTimeoutMs: 2000 }

d('mobile core against the real Go edge', () => {
  it('pairs via a real QR payload, then identity-verifies, logs in and reads live data', async () => {
    // an on-site admin signs in on the Pi and starts pairing
    const login = await (await fetch(`${EDGE}/api/v1/local/auth/login`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username: 'admin', password: PW }) })).json() as { token: string }
    const start = await (await fetch(`${EDGE}/api/v1/site-pairing/start`, { method: 'POST', headers: { Authorization: `Bearer ${login.token}`, 'Content-Type': 'application/json' }, body: '{}' })).json() as { qr_payload: string }
    const qr = parsePairingPayload(start.qr_payload) // the phone's strict parser accepts what the Pi really emits
    expect(qr.hints[0]).toMatch(/^http:\/\/127\.0\.0\.1:\d+$/)

    // 1) verify the Pi (real Ed25519 signature from Go) BEFORE sending the code
    const ch = newChallenge(env.random)
    const idr = await (await fetch(`${EDGE}/api/v1/identity?challenge=${ch}`)).json()
    expect(verifyIdentity({ siteId: qr.site, publicKey: qr.pk }, ch, idr)).toBe(true)
    // a tampered challenge fails
    expect(verifyIdentity({ siteId: qr.site, publicKey: qr.pk }, newChallenge(env.random), idr)).toBe(false)

    // 2) consume the single-use code
    const body = JSON.stringify({ code: qr.code, device_name: 'vitest phone' })
    const ok = await fetch(`${EDGE}/api/v1/site-pairing/confirm`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body })
    expect(ok.status).toBe(200)
    expect((await fetch(`${EDGE}/api/v1/site-pairing/confirm`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body })).status).toBe(401) // single use

    // 3) the Connection Manager picks LOCAL, then a SourceSession reads real units
    const pairing: Pairing = { siteId: qr.site, name: qr.name, publicKey: qr.pk, localHints: qr.hints, pairedAt: 1 }
    expect(await probeLocal(env, pairing)).toMatchObject({ ok: true })
    const decision = await decide({ env, pairing, mode: 'auto', hasSession: async s => s === 'local', cloudReachable: async () => false, cachedFrom: async () => null })
    expect(decision).toMatchObject({ kind: 'live', source: 'local' })
    const s = new SourceSession(new MemoryStore(), new NamespacedCache(new MemoryStore(), Date.now), fetch, Date.now)
    await s.tokens.set(pairing.siteId, 'local', login.token)
    s.switchTo(pairing.siteId, 'local', EDGE!, 'admin')
    const units = await s.get<{ units: { id: string; is_simulated: boolean }[] }>(`/api/v1/sites/${pairing.siteId}/units`)
    expect(units.data.units.length).toBeGreaterThan(0); expect(units.data.units.every(u => u.is_simulated)).toBe(true)
    expect(await s.cached(`/api/v1/sites/${pairing.siteId}/units`)).toMatchObject({ fromCache: true, source: 'local' })

    // a different "Pi" at the same address with another key is rejected by the pinned key
    const wrong: Pairing = { ...pairing, publicKey: 'A'.repeat(43) }
    expect(await probeLocal(env, wrong)).toEqual({ ok: false, reason: 'LOCAL_IDENTITY_MISMATCH' })
  })
})
