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
    <article className={`card fleet-card ${tone}`}>
      <header><h3>{s.name}</h3><span className="fleet-role">{s.role.toLowerCase()}</span></header>
      <div className="fleet-status">{s.stale ? <Badge tone="warn">Pi not syncing · last synced {ago(s.last_sync_at)}</Badge> : <Badge tone="ok">Synced {ago(s.last_sync_at)}</Badge>}</div>
      <dl>
        <div className="fleet-metric"><dt>Units reporting</dt><dd>{s.units_online} of {s.units_total}{s.stale && <span className="fleet-secondary">Last known</span>}</dd></div>
        <div className="fleet-metric"><dt>Units needing attention</dt><dd>{s.units_attention}</dd></div>
        <div><dt>Alerts</dt><dd>{crit ? <b>{crit} critical</b> : '0 critical'} · {warn} warning</dd></div>
        <div><dt>Crop profiles</dt><dd>{s.crops.length ? s.crops.map(c => `${c.crop} (${c.units})`).join(', ') : 'None assigned'}{s.units_without_crop > 0 && <span className="fleet-secondary">{s.units_without_crop} units without a crop profile</span>}</dd></div>
      </dl>
      <button className="fleet-open" onClick={open}>Open {s.name}</button>
    </article>
  )
}

function ConnectPi({ org }: { org: Org }) {
  const [code, setCode] = useState<{ code: string; expires_at: string } | null>(null)
  const [err, setErr] = useState<ApiError | null>(null)
  const make = async () => { setErr(null); try { setCode(await api('POST', `/api/v1/orgs/${org.id}/site-claims`)) } catch (x) { setErr(x as ApiError) } }
  return (
    <section className="panel fleet-connect" aria-label={`Connect a Pi to ${org.name}`}>
      <ErrorNote error={err} />
      {!code ? <><div><h2>Connect a Pi</h2><p className="muted">Add a controller to {org.name} with a one-time connection code.</p></div><button className="primary" onClick={() => void make()}>Create connection code</button></> : (<>
        <p>Connection code: <b style={{ fontSize: '1.4rem', letterSpacing: '.08em' }}>{code.code}</b></p>
        <p className="muted small">Valid until {new Date(code.expires_at).toLocaleString()} and works once. On the Pi's local dashboard open <b>Administration → Connect to cloud</b>, enter <b>{window.location.origin}</b> and this code. The Pi keeps working on its own whether or not it is connected.</p>
        {/^(localhost|127\.0\.0\.1|\[::1\])$/.test(window.location.hostname) && <p className="muted small">This cloud is on your own computer, and “localhost” on the Pi means the Pi itself. In the Docker demo enter <b>http://cloud:8090</b> instead; otherwise use this computer’s network address.</p>}
        <button onClick={() => setCode(null)}>Done</button>
      </>)}
    </section>
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
    <div className="fleet-page">
      <div className="fleet-intro"><h1>Your farms</h1>{fleet.data && <p className="fleet-note muted small">{fleet.data.note}</p>}</div>
      {!session?.site && <p className="muted">Signed in as {session?.user.username}. <button className="link" onClick={() => void signOut()}>Sign out</button></p>}
      <ErrorNote error={fleet.error} retry={fleet.reload} />
      {fleet.loading && <Loading what="your Pis" />}
      {fleet.data && total === 0 && <Empty>No Pi is connected yet.{owned.length ? ' Create a connection code below and enter it on your Pi.' : ' Ask an owner of your organisation to give you access to a Pi.'}</Empty>}
      {fleet.data?.organizations.map(o => (
        <section className="fleet-group" key={o.id} aria-label={o.name}>
          <div className="fleet-group-heading"><h2>{o.name}</h2><span className="muted small">{KIND_TEXT[o.kind]} · {o.sites.length} Pi{o.sites.length === 1 ? '' : 's'}</span></div>
          <div className="cards">{o.sites.map(s => <SiteCard key={s.id} s={s} open={() => void open(s.id)} />)}</div>
        </section>))}
      {owned.map(o => <ConnectPi key={o.id} org={o} />)}
    </div>
  )
}
