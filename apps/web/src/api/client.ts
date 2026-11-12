export class ApiError extends Error {
  status: number
  code: string
  details: Record<string, unknown>
  constructor(status: number, code: string, message: string, details: Record<string, unknown> = {}) {
    super(message)
    this.status = status
    this.code = code
    this.details = details
  }
}

const KEY = 'aerolab_token'
let token: string | null = safeGet()
let onUnauthorized: () => void = () => {}

function safeGet(): string | null { try { return sessionStorage.getItem(KEY) } catch { return null } }
export function setToken(t: string | null) {
  token = t
  try { if (t) sessionStorage.setItem(KEY, t); else sessionStorage.removeItem(KEY) } catch { /* private mode */ }
}
export const hasToken = () => token !== null
export function setUnauthorizedHandler(f: () => void) { onUnauthorized = f }
export const authHeader = (): Record<string, string> => (token ? { Authorization: `Bearer ${token}` } : {})

export async function api<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      method, signal,
      headers: { ...authHeader(), ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}) },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e
    throw new ApiError(0, 'NETWORK', 'Cannot reach the AeroLab Pi. Check that you are on the site Wi-Fi.')
  }
  if (res.status === 204) return undefined as T
  const text = await res.text()
  let json: unknown = null
  try { json = text ? JSON.parse(text) : null } catch { /* non-JSON error page */ }
  if (!res.ok) {
    const e = (json as { error?: { code: string; message: string; details?: Record<string, unknown> } } | null)?.error
    if (res.status === 401 && path !== '/api/v1/local/auth/login') onUnauthorized()
    throw new ApiError(res.status, e?.code ?? 'ERROR', e?.message ?? `Request failed (${res.status})`, e?.details)
  }
  return json as T
}

// ---- Server-Sent Events over fetch (EventSource cannot send Authorization) ----
export interface SseEvent { type: string; data: unknown }
export type StreamState = 'connecting' | 'live' | 'reconnecting'

/** Parses an SSE text buffer; returns complete events and the unconsumed rest. */
export function parseSse(buf: string): { events: SseEvent[]; rest: string } {
  const events: SseEvent[] = []
  const blocks = buf.split('\n\n')
  const rest = blocks.pop() ?? ''
  for (const b of blocks) {
    let type = 'message'; const data: string[] = []
    for (const line of b.split('\n')) {
      if (line.startsWith('event: ')) type = line.slice(7)
      else if (line.startsWith('data: ')) data.push(line.slice(6))
    }
    if (!data.length) continue
    try { events.push({ type, data: JSON.parse(data.join('\n')) }) } catch { /* ignore malformed */ }
  }
  return { events, rest }
}

export function openEvents(siteId: string, onEvent: (e: SseEvent) => void, onState: (s: StreamState) => void): () => void {
  let stop = false; let ctrl: AbortController | null = null; let delay = 1000
  const run = async () => {
    while (!stop) {
      ctrl = new AbortController(); onState(delay === 1000 ? 'connecting' : 'reconnecting')
      try {
        const res = await fetch(`/api/v1/sites/${siteId}/events`, { headers: authHeader(), signal: ctrl.signal })
        if (res.status === 401) { onUnauthorized(); return }
        if (!res.ok || !res.body) throw new Error('stream refused')
        const reader = res.body.getReader(); const dec = new TextDecoder(); let buf = ''
        delay = 1000
        for (;;) {
          const { value, done } = await reader.read()
          if (done) break
          buf += dec.decode(value, { stream: true })
          const p = parseSse(buf); buf = p.rest
          for (const ev of p.events) { if (ev.type === 'hello') onState('live'); onEvent(ev) }
        }
      } catch { /* fall through to reconnect */ }
      if (stop) return
      onState('reconnecting')
      await new Promise(r => setTimeout(r, delay)); delay = Math.min(delay * 2, 15000)
    }
  }
  void run()
  return () => { stop = true; ctrl?.abort() }
}
