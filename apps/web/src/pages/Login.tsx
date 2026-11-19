import { LanguageButton, Tr } from '../app/language'
import { useState, type FormEvent } from 'react'
import { useApp } from '../app/context'
import { ApiError } from '../api/client'
import { IS_CLOUD } from '../api/paths'
import { Signup } from './Signup'
import { useTheme } from '../app/theme'
import { FullLogo } from '../components/Logo'

export function Login() {
  const { theme, toggle } = useTheme()
  const { signIn } = useApp()
  const [u, setU] = useState(''); const [p, setP] = useState(''); const [err, setErr] = useState(''); const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState({ u: false, p: false })
  const [signup, setSignup] = useState(false)
  if (signup) return <Signup back={() => setSignup(false)} />
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
      <LanguageButton />
      <button className="theme-toggle login-theme" type="button" onClick={toggle} aria-label={`Switch to ${theme === 'dark' ? 'light' : 'dark'} mode`}>{theme === 'dark' ? '☀ Light' : '☾ Dark'}</button>
      <form onSubmit={submit} noValidate aria-labelledby="t">
        <h1 id="t"><FullLogo /><span className="sr-only">AeroLab</span></h1>
        <p className="muted">{IS_CLOUD ? <><Tr>Sign in to see all your farms. New here?</Tr>{' '}<button type="button" className="link" onClick={() => setSignup(true)}><Tr>Create an account</Tr></button></> : <Tr>Sign in to this site. This works without internet.</Tr>}</p>
        <label>{IS_CLOUD ? <Tr>{"Email"}</Tr> : <Tr>{"Username"}</Tr>}<input value={u} onChange={e => setU(e.target.value)} onBlur={() => setTouched(t => ({ ...t, u: true }))} autoComplete="username" autoFocus aria-invalid={touched.u && !u} /></label>
        {touched.u && !u && <span className="field-error"><Tr>{IS_CLOUD ? 'Enter your email address.' : 'Enter your username.'}</Tr></span>}
        <label><Tr>Password</Tr><input type="password" value={p} onChange={e => setP(e.target.value)} onBlur={() => setTouched(t => ({ ...t, p: true }))} autoComplete="current-password" aria-invalid={touched.p && !p} /></label>
        {touched.p && !p && <span className="field-error"><Tr>Enter your password.</Tr></span>}
        {err && <div className="note error" role="alert">{err}</div>}
        <button className="primary" disabled={busy}><Tr>{busy ? 'Signing in…' : 'Sign in'}</Tr></button>
      </form>
    </div>
  )
}
