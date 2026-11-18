import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import { useApp } from '../app/context'
import { useFetch } from '../app/hooks'
import type { Incident } from '../api/types'
import { P } from '../api/paths'
import { ago, clock } from '../app/format'
import { ErrorNote, Loading } from '../components/ui'
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

      {list.data && list.data.incidents.length > 0 && <ul className="incident-list">
        {list.data.incidents.map(i => (
          <li key={i.id} className={`incident-item ${i.severity}`}>
            <div className="incident-head">
              <div className="incident-title">
                <span className="incident-severity-icon"><Icon name={i.severity === 'critical' ? 'critical' : i.severity === 'warning' ? 'warning' : 'alerts'} size={19} /></span>
                <div>
                  <div className="incident-labels"><span className={`incident-severity ${i.severity}`}>{i.severity}</span><span className="incident-code">{i.code.replace(/_/g, ' ')}</span></div>
                  <h2>{i.title}</h2>
                </div>
              </div>
              <Link className="incident-unit" to={`/units/${i.unit_id}`}>{i.unit_id}<Icon name="arrow" size={15} /></Link>
            </div>
            <div className="incident-body">
              <div><span className="incident-field-label">Cause</span><p>{i.cause}</p></div>
              <div><span className="incident-field-label">Suggested action</span><p>{i.suggested_action}</p></div>
            </div>
            {i.note && <p className="incident-note">{i.note}</p>}
            <footer className="incident-footer">
              <div className="incident-times">
                <span>First seen {clock(i.first_seen_at)}</span>
                <span>Last seen {ago(i.last_seen_at)}</span>
                {i.resolved_at && <span>Resolved {clock(i.resolved_at)}</span>}
                {i.acknowledged_at && <span>Acknowledged {clock(i.acknowledged_at)}</span>}
              </div>
              {i.status.endsWith('UNACKNOWLEDGED') && can('ack_incident') && <button type="button" className="secondary" onClick={() => void ack(i.id)}>Acknowledge</button>}
            </footer>
          </li>
        ))}
      </ul>}

      {state === 'active' && list.data && list.data.incidents.length > 0 &&
        <p className="incidents-explainer"><Icon name="alerts" size={15} /> Acknowledging marks an alert as seen. The controller resolves it when the fault clears.</p>}
    </section>
  )
}
