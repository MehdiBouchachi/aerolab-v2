import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import { useApp } from '../app/context'
import { useFetch } from '../app/hooks'
import type { Fleet as FleetT, FleetSite, Org, OrgKind } from '../api/types'
import { ago } from '../app/format'
import { Badge, Empty, ErrorNote, Loading } from '../components/ui'

export const KIND_TEXT: Record<OrgKind, string> = { PERSONAL: 'Personal', BUSINESS: 'Farm / company', GOVERNMENT: 'Public institution' }

function SiteCard({ s, open }: { s: FleetSite; open: () => void }) {
  const crit = s.active_incidents.critical ?? 0, warn = s.active_incidents.warning ?? 0
  const tone = s.stale ? 'warn' : crit ? 'crit' : s.units_attention ? 'warn' : 'ok'
  return (
    <article className={`card ${tone}`}>
      <header><h3>{s.name}</h3><span className="muted small">{s.role.toLowerCase()}</span></header>
      {s.stale ? <Badge tone="warn">Pi not syncing – last synced {ago(s.last_sync_at)}</Badge> : <Badge tone="ok">Synced {ago(s.last_sync_at)}</Badge>}
      <dl>
        <dt>Units</dt><dd>{s.units_online} of {s.units_total} reporting{s.stale && ' (last known)'}</dd>
        <dt>Need attention</dt><dd>{s.units_attention}</dd>
        <dt>Alerts</dt><dd>{crit ? <b>{crit} critical</b> : '0 critical'} · {warn} warning</dd>
        <dt>Growing</dt><dd>{s.crops.length ? s.crops.map(c => `${c.crop} (${c.units})`).join(', ') : '–'}{s.units_without_crop ? ` · ${s.units_without_crop} without a crop profile` : ''}</dd>
      </dl>
      <button onClick={open}>Open {s.name}</button>
    </article>
  )
}

function ConnectPi({ org }: { org: Org }) {
  const [code, setCode] = useState<{ code: string; expires_at: string } | null>(null)
  const [err, setErr] = useState<ApiError | null>(null)
  const make = async () => { setErr(null); try { setCode(await api('POST', `/api/v1/orgs/${org.id}/site-claims`)) } catch (x) { setErr(x as ApiError) } }
  return (
    <div className="panel" style={{ margin: '.75rem 0' }}>
      <ErrorNote error={err} />
      {!code ? <button className="primary" onClick={() => void make()}>Connect a Pi to {org.name}</button> : (<>
        <p>Connection code: <b style={{ fontSize: '1.4rem', letterSpacing: '.08em' }}>{code.code}</b></p>
        <p className="muted small">Valid until {new Date(code.expires_at).toLocaleString()} and works once. On the Pi's local dashboard open <b>Administration → Connect to cloud</b>, enter <b>{window.location.origin}</b> and this code. The Pi keeps working on its own whether or not it is connected.</p>
        {/^(localhost|127\.0\.0\.1|\[::1\])$/.test(window.location.hostname) && <p className="muted small">This cloud is on your own computer, and “localhost” on the Pi means the Pi itself. In the Docker demo enter <b>http://cloud:8090</b> instead; otherwise use this computer’s network address.</p>}
        <button onClick={() => setCode(null)}>Done</button>
      </>)}
    </div>
  )
}

/** Every Pi a person may see, across organisations (cloud only). Works with zero sites (onboarding). */
export function Fleet() {
  const { selectSite, session, signOut } = useApp()
  const nav = useNavigate()
  const fleet = useFetch<FleetT>('/api/v1/fleet', [], 15000)
  const orgs = useFetch<{ organizations: Org[] }>('/api/v1/orgs', [], 0)
  const owned = orgs.data?.organizations.filter(o => o.role === 'OWNER') ?? []
  const open = async (id: string) => { await selectSite(id); nav('/site') }
  const total = fleet.data?.organizations.reduce((n, o) => n + o.sites.length, 0) ?? 0
  return (
    <>
      <h1>Your farms</h1>
      {!session?.site && <p className="muted">Signed in as {session?.user.username}. <button className="link" onClick={() => void signOut()}>Sign out</button></p>}
      <ErrorNote error={fleet.error} retry={fleet.reload} />
      {fleet.loading && <Loading what="your Pis" />}
      {fleet.data && <p className="muted small">{fleet.data.note}</p>}
      {fleet.data && total === 0 && <Empty>No Pi is connected yet.{owned.length ? ' Create a connection code below and enter it on your Pi.' : ' Ask an owner of your organisation to give you access to a Pi.'}</Empty>}
      {fleet.data?.organizations.map(o => (
        <section key={o.id} aria-label={o.name}>
          <h2>{o.name} <span className="muted small">· {KIND_TEXT[o.kind]} · {o.sites.length} Pi{o.sites.length === 1 ? '' : 's'}</span></h2>
          <div className="cards">{o.sites.map(s => <SiteCard key={s.id} s={s} open={() => void open(s.id)} />)}</div>
        </section>))}
      {owned.map(o => <ConnectPi key={o.id} org={o} />)}
    </>
  )
}
