import * as ed from '@noble/ed25519'
import { sha512 } from '@noble/hashes/sha2.js'
import type { Pairing } from './types'

ed.hashes.sha512 = sha512 // RN has no crypto.subtle: use the pure-JS hash

export const MSG_PREFIX = 'aerolab-identity-v1\n' // must equal internal/pairing MsgPrefix on the Pi
const ID_RE = /^[a-z0-9][a-z0-9-]{2,39}$/

export function b64urlEncode(b: Uint8Array): string {
  let s = ''
  for (const x of b) s += String.fromCharCode(x)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}
export function b64urlDecode(s: string): Uint8Array {
  if (!/^[A-Za-z0-9_-]*$/.test(s)) throw new Error('bad base64url')
  const p = s.replace(/-/g, '+').replace(/_/g, '/') + '==='.slice((s.length + 3) % 4)
  const bin = atob(p)
  return Uint8Array.from(bin, c => c.charCodeAt(0))
}

export class PairingError extends Error {
  constructor(public code: 'MALFORMED' | 'UNSUPPORTED_VERSION' | 'INVALID_SITE' | 'INVALID_KEY' | 'INVALID_HINT' | 'INVALID_CODE' | 'TOO_LARGE', message: string) {
    super(message)
  }
}

/** Private-network / lab hosts for which plain http is tolerated (and flagged as unencrypted in the UI). */
export function isLabHost(host: string): boolean {
  if (host === 'localhost' || host.endsWith('.local')) return true
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host)
  if (!m) return false
  const [a, b] = [Number(m[1]), Number(m[2])]
  return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168)
}

export interface Hint { url: string; insecureLab: boolean }

/** Accept https anywhere; http only for private/lab hosts. Never any other scheme, never credentials in the URL. */
export function acceptableHint(raw: string): Hint | null {
  if (raw.length > 200) return null
  let u: URL
  try { u = new URL(raw) } catch { return null }
  if (u.username || u.password || u.search || u.hash) return null
  if (u.protocol === 'https:') return { url: u.origin, insecureLab: false }
  if (u.protocol === 'http:' && isLabHost(u.hostname)) return { url: u.origin, insecureLab: true }
  return null
}

export interface PairingPayload { v: 1; site: string; name: string; pk: string; hints: string[]; code: string }

/** Parse and strictly validate the QR payload shown by the Pi. Untrusted input. */
export function parsePairingPayload(raw: string): PairingPayload {
  if (raw.length > 2048) throw new PairingError('TOO_LARGE', 'QR payload too large')
  let o: Record<string, unknown>
  try { o = JSON.parse(raw) as Record<string, unknown> } catch { throw new PairingError('MALFORMED', 'Not an AeroLab pairing code') }
  if (typeof o !== 'object' || o === null) throw new PairingError('MALFORMED', 'Not an AeroLab pairing code')
  if (o.v !== 1) throw new PairingError('UNSUPPORTED_VERSION', 'This pairing code needs a newer app version')
  if (typeof o.site !== 'string' || !ID_RE.test(o.site)) throw new PairingError('INVALID_SITE', 'Invalid site id')
  if (typeof o.pk !== 'string') throw new PairingError('INVALID_KEY', 'Missing site key')
  try { if (b64urlDecode(o.pk).length !== 32) throw new Error() } catch { throw new PairingError('INVALID_KEY', 'Invalid site key') }
  if (typeof o.code !== 'string' || !/^[A-Z2-9]{4}-[A-Z2-9]{4}$/i.test(o.code)) throw new PairingError('INVALID_CODE', 'Invalid pairing code')
  const hints = Array.isArray(o.hints) ? o.hints.filter((h): h is string => typeof h === 'string') : []
  const ok = hints.map(acceptableHint).filter((h): h is Hint => h !== null)
  if (ok.length === 0) throw new PairingError('INVALID_HINT', 'No usable address in the pairing code (https, or http on a private network)')
  const name = typeof o.name === 'string' ? o.name.slice(0, 60) : o.site
  return { v: 1, site: o.site, name, pk: o.pk, hints: ok.map(h => h.url), code: o.code.toUpperCase() }
}

export interface IdentityResponse { site_id: string; public_key: string; signature: string }

/**
 * Verify a Pi's answer to OUR random challenge against the key pinned at pairing time.
 * All three must hold: same site id, same pinned key, valid signature over (site, our challenge).
 */
export function verifyIdentity(p: Pick<Pairing, 'siteId' | 'publicKey'>, challenge: string, r: IdentityResponse): boolean {
  try {
    if (r.site_id !== p.siteId || r.public_key !== p.publicKey) return false
    const msg = new TextEncoder().encode(MSG_PREFIX + p.siteId + '\n' + challenge)
    return ed.verify(b64urlDecode(r.signature), msg, b64urlDecode(r.public_key))
  } catch { return false }
}

export function newChallenge(random: (n: number) => Uint8Array): string { return b64urlEncode(random(16)) }
