import type { ReactNode } from 'react'
import type { Tone } from '../app/format'
import { ApiError } from '../api/client'

export const Badge = ({ tone, children }: { tone: Tone; children: ReactNode }) =>
  <span className={`badge ${tone}`}>{tone === 'ok' ? '✔ ' : tone === 'crit' ? '■ ' : tone === 'warn' ? '▲ ' : ''}{children}</span>
export const Sim = () => <span className="sim" title="Data from the simulator, not real hardware">SIMULATED</span>

export function ErrorNote({ error, retry }: { error: ApiError | null; retry?: () => void }) {
  if (!error) return null
  return <div className="note error" role="alert">{error.message}{retry && <> <button className="link" onClick={retry}>Try again</button></>}</div>
}
export const Loading = ({ what }: { what: string }) => <p className="muted" role="status">Loading {what}…</p>
export const Empty = ({ children }: { children: ReactNode }) => <p className="empty">{children}</p>
