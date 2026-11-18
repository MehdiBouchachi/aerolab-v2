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
          <h1 id="alerts-title">Alerts</h1>
          <p className="muted">Controller faults and their resolution history.</p>
        </div>
      </header>

      <div className="incidents-tabs" role="group" aria-label="Alert status">
        <button type="button" aria-pressed={state === 'active'} onClick={() => setState('active')}>Active{state === 'active' && list.data && list.data.incidents.length > 0 && <span className="incidents-count">{list.data.incidents.length}</span>}</button>
        <button type="button" aria-pressed={state === 'resolved'} onClick={() => setState('resolved')}>History</button>
      </div>

      <ErrorNote error={err ?? list.error} retry={list.reload} />
      {list.loading && <Loading what="alerts" />}
      {list.data && list.data.incidents.length === 0 && (
        <div className="incidents-empty" role="status">
          <div className="incidents-empty-icon"><Icon name={state === 'active' ? 'check' : 'clock'} size={23} /></div>
          <div>
            <h2>{state === 'active' ? 'No active alerts' : 'No resolved alerts yet'}</h2>
            <p>{state === 'active' ? 'Reported controller faults appear here.' : 'Alerts move here when the controller clears the fault.'}</p>
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
                  <Link to={`/units/${i.unit_id}`}>View unit <Icon name="arrow" size={13} /></Link>
                  <span>{i.resolved_at ? `Resolved ${ago(i.resolved_at)}` : `Last seen ${ago(i.last_seen_at)}`}</span>
                  {i.acknowledged_at && <span>Acknowledged</span>}
                </div>
              </div>
              <Badge tone={i.severity === 'critical' ? 'crit' : i.severity === 'warning' ? 'warn' : 'muted'}>{i.severity === 'critical' ? 'Critical' : i.severity === 'warning' ? 'Warning' : 'Info'}</Badge>
            </div>
            <div className="incident-entry-actions">
              <details>
                <summary>Alert details</summary>
                <div className="incident-entry-detail">
                  <div><strong>Suggested action</strong><p>{i.suggested_action}</p></div>
                  <div><strong>Cause</strong><p>{i.cause}</p></div>
                  {i.note && <p>{i.note}</p>}
                  <p className="muted small">First seen {clock(i.first_seen_at)} · {i.unit_id} · {i.code}</p>
                  {i.resolved_at && <p className="muted small">Resolved {clock(i.resolved_at)}</p>}
                  {i.acknowledged_at && <p className="muted small">Acknowledged {clock(i.acknowledged_at)}</p>}
                </div>
              </details>
              {i.status.endsWith('UNACKNOWLEDGED') && can('ack_incident') && <button type="button" className="secondary" onClick={() => void ack(i.id)}>Acknowledge</button>}
            </div>
          </li>
        ))}
      </ul>}

      {state === 'active' && can('ack_incident') && list.data?.incidents.some(i => i.status.endsWith('UNACKNOWLEDGED')) &&
        <p className="incidents-explainer"><Icon name="alerts" size={15} /> Acknowledging marks an alert as seen. The controller resolves it when the fault clears.</p>}
    </section>
  )
}
