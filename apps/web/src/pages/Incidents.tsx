import { Tr } from '../app/language'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import { useApp } from '../app/context'
import { useFetch } from '../app/hooks'
import type { Incident } from '../api/types'
import { P } from '../api/paths'
import { ago, clock, incidentTitle } from '../app/format'
import { Badge, ErrorNote, Loading } from '../components/ui'
import { Icon } from '../components/Icon'

export function Incidents() {
  const { session, can } = useApp()
  const site = session!.site!.id
  const [state, setState] = useState<'active' | 'resolved'>('active')
  const [err, setErr] = useState<ApiError | null>(null)
  const list = useFetch<{ incidents: Incident[] }>(`/api/v1/sites/${site}/incidents?state=${state}&limit=100`, ['incident.updated'])
  const ack = async (id: string) => {
    setErr(null)
    try { await api('POST', P.site(site, `/api/v1/incidents/${id}/acknowledge`)); list.reload() }
    catch (e) { setErr(e as ApiError) }
  }

  return (
    <section className="incidents-page" aria-labelledby="alerts-title">
      <header className="incidents-heading">
        <div>
          <h1 id="alerts-title"><Tr>Alerts</Tr></h1>
          <p className="muted"><Tr>Controller faults and their resolution history.</Tr></p>
        </div>
      </header>

      <div className="incidents-tabs" role="group" aria-label="Alert status">
        <button type="button" aria-pressed={state === 'active'} onClick={() => setState('active')}><Tr>Active</Tr>{state === 'active' && list.data && list.data.incidents.length > 0 && <span className="incidents-count">{list.data.incidents.length}</span>}</button>
        <button type="button" aria-pressed={state === 'resolved'} onClick={() => setState('resolved')}><Tr>History</Tr></button>
      </div>

      <ErrorNote error={err ?? list.error} retry={list.reload} />
      {list.loading && <Loading what="alerts" />}
      {list.data && list.data.incidents.length === 0 && (
        <div className="incidents-empty" role="status">
          <div className="incidents-empty-icon"><Icon name={state === 'active' ? 'check' : 'clock'} size={23} /></div>
          <div>
            <h2>{state === 'active' ? <Tr>{"No active alerts"}</Tr> : <Tr>{"No resolved alerts yet"}</Tr>}</h2>
            <p>{state === 'active' ? <Tr>{"Reported controller faults appear here."}</Tr> : <Tr>{"Alerts move here when the controller clears the fault."}</Tr>}</p>
          </div>
        </div>
      )}

      {list.data && list.data.incidents.length > 0 && <ul className="incident-list incident-list-surface">
        {list.data.incidents.map(i => (
          <li key={i.id} className="incident-entry">
            <div className="incident-entry-header">
              <div>
                <h2>{incidentTitle(i.title)}</h2>
                <div className="incident-entry-meta">
                  <Link to={`/units/${i.unit_id}`}><Tr>View unit</Tr> <Icon name="arrow" size={13} /></Link>
                  <span>{i.resolved_at ? `Resolved ${ago(i.resolved_at)}` : `Last seen ${ago(i.last_seen_at)}`}</span>
                  {i.acknowledged_at && <span><Tr>Acknowledged</Tr></span>}
                </div>
              </div>
              <Badge tone={i.severity === 'critical' ? 'crit' : i.severity === 'warning' ? 'warn' : 'muted'}>{i.severity === 'critical' ? <Tr>{"Critical"}</Tr> : i.severity === 'warning' ? <Tr>{"Warning"}</Tr> : <Tr>{"Info"}</Tr>}</Badge>
            </div>
            <div className="incident-entry-actions">
              <details>
                <summary><Tr>Alert details</Tr></summary>
                <div className="incident-entry-detail">
                  <div><strong><Tr>Suggested action</Tr></strong><p>{i.suggested_action}</p></div>
                  <div><strong><Tr>Cause</Tr></strong><p>{i.cause}</p></div>
                  {i.note && <p>{i.note}</p>}
                  <p className="muted small">First seen {clock(i.first_seen_at)} · {i.unit_id} · {i.code}</p>
                  {i.resolved_at && <p className="muted small">Resolved {clock(i.resolved_at)}</p>}
                  {i.acknowledged_at && <p className="muted small"><Tr>Acknowledged</Tr> {clock(i.acknowledged_at)}</p>}
                </div>
              </details>
              {i.status.endsWith('UNACKNOWLEDGED') && can('ack_incident') && <button type="button" className="secondary" onClick={() => void ack(i.id)}><Tr>Acknowledge</Tr></button>}
            </div>
          </li>
        ))}
      </ul>}

      {state === 'active' && can('ack_incident') && list.data?.incidents.some(i => i.status.endsWith('UNACKNOWLEDGED')) &&
        <p className="incidents-explainer"><Icon name="alerts" size={15} /> Acknowledging marks an alert as seen. The controller resolves it when the fault clears.</p>}
    </section>
  )
}
