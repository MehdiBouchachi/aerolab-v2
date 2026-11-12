import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import { useApp } from '../app/context'
import { useFetch } from '../app/hooks'
import type { Incident } from '../api/types'
import { ago, clock } from '../app/format'
import { Badge, Empty, ErrorNote, Loading } from '../components/ui'

export function Incidents() {
  const { session, can } = useApp(); const site = session!.site.id
  const [state, setState] = useState<'active' | 'resolved'>('active')
  const [err, setErr] = useState<ApiError | null>(null)
  const list = useFetch<{ incidents: Incident[] }>(`/api/v1/sites/${site}/incidents?state=${state}&limit=100`, ['incident.updated'])
  const ack = async (id: string) => { setErr(null); try { await api('POST', `/api/v1/incidents/${id}/acknowledge`); list.reload() } catch (e) { setErr(e as ApiError) } }
  return (
    <>
      <h1>Alerts</h1>
      <div className="seg" role="group" aria-label="Filter">{(['active', 'resolved'] as const).map(s => <button key={s} aria-pressed={state === s} onClick={() => setState(s)}>{s === 'active' ? 'Active' : 'Resolved (history)'}</button>)}</div>
      <ErrorNote error={err ?? list.error} retry={list.reload} />
      {list.loading && <Loading what="alerts" />}
      {list.data && list.data.incidents.length === 0 && <Empty>{state === 'active' ? 'No active alerts. All reporting units are free of known faults.' : 'No resolved alerts yet.'}</Empty>}
      <ul className="alerts">
        {list.data?.incidents.map(i => (
          <li key={i.id} className={i.severity}>
            <Badge tone={i.severity === 'critical' ? 'crit' : i.severity === 'warning' ? 'warn' : 'muted'}>{i.severity}</Badge> <b>{i.title}</b> — <Link to={`/units/${i.unit_id}`}>{i.unit_id}</Link>
            <div>{i.cause}</div>
            <div className="muted">What to do: {i.suggested_action}</div>
            <div className="muted small">First seen {clock(i.first_seen_at)} · last seen {ago(i.last_seen_at)} · {i.status.replace(/_/g, ' ').toLowerCase()}{i.resolved_at && ` · resolved ${clock(i.resolved_at)}`}</div>
            {i.status.endsWith('UNACKNOWLEDGED') && can('ack_incident') && <button onClick={() => void ack(i.id)}>Acknowledge – I have seen this</button>}
          </li>))}
      </ul>
      <p className="muted small">Acknowledging records that a person has seen an alert. It does not fix the problem; an alert resolves only when the controller stops reporting the fault.</p>
    </>
  )
}
