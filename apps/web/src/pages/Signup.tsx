import { useState, type FormEvent } from 'react'
import { useApp } from '../app/context'
import { ApiError } from '../api/client'
import type { OrgKind } from '../api/types'
import { FullLogo } from '../components/Logo'

const KINDS: { k: OrgKind; title: string; hint: string }[] = [
  { k: 'PERSONAL', title: 'For myself', hint: 'A home or hobby setup' },
  { k: 'BUSINESS', title: 'Farm or company', hint: 'Several people and sites' },
  { k: 'GOVERNMENT', title: 'Public institution', hint: 'Research, extension service, ministry' },
]

export function Signup({ back }: { back: () => void }) {
  const { signUp } = useApp()
  const [f, setF] = useState({ email: '', password: '', kind: 'PERSONAL' as OrgKind, org: '' })
  const [touched, setTouched] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const errs = { email: !f.email.includes('@'), password: f.password.length < 12, org: f.kind !== 'PERSONAL' && !f.org.trim() }
  const submit = async (e: FormEvent) => {
    e.preventDefault(); setTouched(true)
    if (errs.email || errs.password || errs.org) return
    setBusy(true); setErr('')
    try { await signUp({ email: f.email, password: f.password, organization_kind: f.kind, organization_name: f.org.trim() }) } catch (x) {
      const a = x as ApiError
      setErr(a.code === 'SIGNUP_DISABLED' ? 'Accounts on this service are created by its operator. Ask them for an invitation.' : a.code === 'ACCOUNT_EXISTS' ? 'An account with this email already exists. Sign in instead.' : a.message)
    } finally { setBusy(false) }
  }
  return (
    <div className="login">
      <form onSubmit={submit} noValidate aria-labelledby="st">
        <FullLogo />
        <h1 id="st">Create an AeroLab account</h1>
        <fieldset><legend className="small">Who is this account for?</legend>
          {KINDS.map(x => <label key={x.k} style={{ flexDirection: 'row', gap: '.5rem', alignItems: 'center' }}>
            <input type="radio" name="kind" checked={f.kind === x.k} onChange={() => setF({ ...f, kind: x.k })} style={{ minHeight: 0 }} /> <span><b>{x.title}</b> <span className="muted small">{x.hint}</span></span></label>)}
        </fieldset>
        {f.kind !== 'PERSONAL' && <label>Organisation name<input value={f.org} onChange={e => setF({ ...f, org: e.target.value })} aria-invalid={touched && errs.org} /></label>}
        {touched && errs.org && <span className="field-error">Enter the organisation's name.</span>}
        <label>Email<input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} autoComplete="email" aria-invalid={touched && errs.email} /></label>
        {touched && errs.email && <span className="field-error">Enter your email address.</span>}
        <label>Password (12+ characters)<input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} autoComplete="new-password" aria-invalid={touched && errs.password} /></label>
        {touched && errs.password && <span className="field-error">Use at least 12 characters.</span>}
        {err && <div className="note error" role="alert">{err}</div>}
        <button className="primary" disabled={busy}>{busy ? 'Creating…' : 'Create account'}</button>
        <button type="button" className="link" onClick={back}>I already have an account</button>
      </form>
    </div>
  )
}
