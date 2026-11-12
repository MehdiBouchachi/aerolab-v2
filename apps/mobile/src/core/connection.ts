import { acceptableHint, newChallenge, verifyIdentity, type IdentityResponse } from './identity'
import type { Decision, Env, Mode, Pairing, Reason, Source } from './types'

export type Probe = { ok: true; baseUrl: string; insecureLab: boolean } | { ok: false; reason: 'LOCAL_UNREACHABLE' | 'LOCAL_IDENTITY_MISMATCH' }

async function fetchJson(env: Env, url: string): Promise<unknown> {
  const ctl = new AbortController()
  const t = setTimeout(() => ctl.abort(), env.probeTimeoutMs)
  try {
    const r = await env.fetch(url, { signal: ctl.signal, headers: { Accept: 'application/json' } })
    if (!r.ok) throw new Error(`HTTP ${r.status}`)
    return await r.json()
  } finally { clearTimeout(t) }
}

/**
 * Probe each remembered local address with a SHORT timeout and a fresh random challenge, and accept it
 * only if the answer verifies against the key pinned at pairing. A host that answers but fails
 * verification is an impostor or a different site: that is a hard negative, never "use it anyway".
 */
export async function probeLocal(env: Env, p: Pairing): Promise<Probe> {
  let mismatch = false
  for (const raw of p.localHints) {
    const hint = acceptableHint(raw)
    if (!hint) continue
    const challenge = newChallenge(env.random)
    try {
      const r = (await fetchJson(env, `${hint.url}/api/v1/identity?challenge=${encodeURIComponent(challenge)}`)) as IdentityResponse
      if (verifyIdentity(p, challenge, r)) return { ok: true, baseUrl: hint.url, insecureLab: hint.insecureLab }
      mismatch = true
    } catch { /* unreachable / timeout: try the next hint */ }
  }
  return { ok: false, reason: mismatch ? 'LOCAL_IDENTITY_MISMATCH' : 'LOCAL_UNREACHABLE' }
}

export interface Inputs {
  env: Env
  pairing: Pairing
  mode: Mode
  hasSession: (s: Source) => Promise<boolean>
  cloudReachable: () => Promise<boolean>
  cachedFrom: () => Promise<{ source: Source; at: number } | null>
}

/**
 * The Connection Manager decision (spec 7.4). Pure given its inputs:
 *  - auto: verified local + local session -> LOCAL; else cloud session + reachable -> CLOUD;
 *          else read-only CACHE (with its age); else unavailable.
 *  - local-only: never touches the cloud. cloud-only: never touches the local network.
 * "live" is the only outcome that permits control; cached/unavailable can never send a command.
 */
export async function decide(i: Inputs): Promise<Decision> {
  const unavailableOrCached = async (reason: Reason, needsLocalLogin: boolean, allowed: Source[]): Promise<Decision> => {
    const c = await i.cachedFrom()
    if (c && allowed.includes(c.source)) return { kind: 'cached', reason, canControl: false, needsLocalLogin, cachedFrom: c.source, cachedAt: c.at }
    return { kind: 'unavailable', reason, canControl: false, needsLocalLogin }
  }

  let local: Probe | null = null
  const tryLocal = async () => (local ??= await probeLocal(i.env, i.pairing))

  if (i.mode === 'cloud') {
    if (!(await i.hasSession('cloud'))) return unavailableOrCached('CLOUD_LOGIN_REQUIRED', false, ['cloud'])
    if (!(await i.cloudReachable())) return unavailableOrCached('CLOUD_UNREACHABLE', false, ['cloud'])
    if (!i.pairing.cloudUrl) return unavailableOrCached('CLOUD_ONLY_MODE_NO_CLOUD', false, ['cloud'])
    return { kind: 'live', source: 'cloud', baseUrl: i.pairing.cloudUrl, insecureLab: false, canControl: true, needsLocalLogin: false }
  }

  const l = await tryLocal()
  if (l.ok) {
    if (await i.hasSession('local')) return { kind: 'live', source: 'local', baseUrl: l.baseUrl, insecureLab: l.insecureLab, canControl: true, needsLocalLogin: false }
    if (i.mode === 'local') return { kind: 'unavailable', reason: 'LOCAL_LOGIN_REQUIRED', canControl: false, needsLocalLogin: true }
  } else if (i.mode === 'local') {
    return unavailableOrCached(l.reason === 'LOCAL_IDENTITY_MISMATCH' ? 'LOCAL_IDENTITY_MISMATCH' : 'LOCAL_ONLY_MODE_NO_LOCAL', false, ['local'])
  }

  // auto: local not usable -> cloud, if there is a cloud session and the cloud answers
  const needsLocalLogin = l.ok // local is reachable but we have no local session: offer a login
  if (i.pairing.cloudUrl && (await i.hasSession('cloud')) && (await i.cloudReachable())) {
    return { kind: 'live', source: 'cloud', baseUrl: i.pairing.cloudUrl, insecureLab: false, canControl: true, needsLocalLogin: false }
  }
  const reason: Reason = l.ok ? 'LOCAL_LOGIN_REQUIRED' : l.reason === 'LOCAL_IDENTITY_MISMATCH' ? 'LOCAL_IDENTITY_MISMATCH' : 'NO_SOURCE_AVAILABLE'
  return unavailableOrCached(reason, needsLocalLogin, ['local', 'cloud'])
}
