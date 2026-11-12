import { useState, type FormEvent } from 'react'
import { useApp } from '../app/context'
import { ApiError } from '../api/client'

export function Login() {
  const { signIn } = useApp()
  const [u, setU] = useState(''); const [p, setP] = useState(''); const [err, setErr] = useState(''); const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState({ u: false, p: false })
  const submit = async (e: FormEvent) => {
    e.preventDefault(); setTouched({ u: true, p: true })
    if (!u || !p) return
    setBusy(true); setErr('')
    try { await signIn(u, p) } catch (x) {
      const a = x as ApiError
      setErr(a.status === 429 ? 'Too many attempts. Wait a few minutes and try again.' : a.status === 401 ? 'That username and password do not match.' : a.message)
    } finally { setBusy(false) }
  }
  return (
    <div className="login">
      <form onSubmit={submit} noValidate aria-labelledby="t">
        <h1 id="t">AeroLab</h1>
        <p className="muted">Sign in to this site. This works without internet.</p>
        <label>Username<input value={u} onChange={e => setU(e.target.value)} onBlur={() => setTouched(t => ({ ...t, u: true }))} autoComplete="username" autoFocus aria-invalid={touched.u && !u} /></label>
        {touched.u && !u && <span className="field-error">Enter your username.</span>}
        <label>Password<input type="password" value={p} onChange={e => setP(e.target.value)} onBlur={() => setTouched(t => ({ ...t, p: true }))} autoComplete="current-password" aria-invalid={touched.p && !p} /></label>
        {touched.p && !p && <span className="field-error">Enter your password.</span>}
        {err && <div className="note error" role="alert">{err}</div>}
        <button className="primary" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
      </form>
    </div>
  )
}
