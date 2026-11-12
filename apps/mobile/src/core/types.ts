export type Source = 'local' | 'cloud'
export type Mode = 'auto' | 'local' | 'cloud'

/** What the app remembers about one paired site. No long-term secret lives here. */
export interface Pairing {
  siteId: string
  name: string
  /** Ed25519 public key (base64url) pinned at pairing time. A different Pi cannot pass identity checks. */
  publicKey: string
  localHints: string[]
  cloudUrl?: string
  pairedAt: number
}

export interface KeyValueStore {
  get(key: string): Promise<string | null>
  set(key: string, value: string): Promise<void>
  remove(key: string): Promise<void>
  keys?(): Promise<string[]>
}

/** Secrets (session tokens) belong in the OS keychain; the interface is separate from the cache on purpose. */
export type SecretStore = KeyValueStore

export interface Env {
  fetch: typeof fetch
  now: () => number
  random: (n: number) => Uint8Array
  probeTimeoutMs: number
}

export type Decision =
  | { kind: 'live'; source: Source; baseUrl: string; insecureLab: boolean; canControl: true; needsLocalLogin: false }
  | { kind: 'cached'; reason: Reason; canControl: false; needsLocalLogin: boolean; cachedFrom: Source | null; cachedAt: number | null }
  | { kind: 'unavailable'; reason: Reason; canControl: false; needsLocalLogin: boolean }

export type Reason =
  | 'LOCAL_UNREACHABLE'
  | 'LOCAL_IDENTITY_MISMATCH'
  | 'LOCAL_LOGIN_REQUIRED'
  | 'LOCAL_ONLY_MODE_NO_LOCAL'
  | 'CLOUD_ONLY_MODE_NO_CLOUD'
  | 'CLOUD_LOGIN_REQUIRED'
  | 'CLOUD_UNREACHABLE'
  | 'NO_SOURCE_AVAILABLE'
