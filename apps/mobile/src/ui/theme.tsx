// Visual tokens mirror apps/web/src/styles/dashboard.css (:root and :root[data-theme=dark]).
// One blue for brand, navigation and actions; amber only for attention; red only for critical.
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { StyleSheet, useColorScheme } from 'react-native'
import { cacheStore } from '../native'

export const light = {
  bg: '#fafbfc', panel: '#ffffff', ink: '#182331', muted: '#667386', line: '#e5e9ee',
  brand: '#2857bd', brandSoft: '#eef3fc', onBrand: '#ffffff',
  attention: '#a76412', attentionSoft: '#fff4e5', danger: '#b23b44', dangerSoft: '#fcecee',
  segTrack: '#edf0f3', segActive: '#ffffff', backdrop: 'rgba(11,18,32,0.4)',
}
export type Palette = typeof light
export const dark: Palette = {
  bg: '#141a22', panel: '#1c2530', ink: '#edf1f6', muted: '#aab7c5', line: '#34404e',
  brand: '#a8bcff', brandSoft: '#27364f', onBrand: '#15213b',
  attention: '#f5c47d', attentionSoft: '#3d3024', danger: '#ffa3a9', dangerSoft: '#422b33',
  segTrack: '#141a22', segActive: '#323c48', backdrop: 'rgba(0,0,0,0.55)',
}

export type ThemeChoice = 'system' | 'light' | 'dark'
const KEY = 'theme'

function makeStyles(c: Palette) {
  return StyleSheet.create({
    screen: { flex: 1, backgroundColor: c.bg },
    pad: { paddingHorizontal: 16, paddingTop: 14, paddingBottom: 28, gap: 12 },
    h1: { fontSize: 26, lineHeight: 30, fontWeight: '800', letterSpacing: -0.6, color: c.ink },
    h2: { fontSize: 17, fontWeight: '700', letterSpacing: -0.2, color: c.ink },
    h3: { fontSize: 16, fontWeight: '700', color: c.ink },
    text: { fontSize: 15, color: c.ink },
    strong: { fontSize: 15, fontWeight: '700', color: c.ink },
    muted: { fontSize: 13, color: c.muted },
    small: { fontSize: 11.5, color: c.muted },
    panel: { backgroundColor: c.panel, borderColor: c.line, borderWidth: StyleSheet.hairlineWidth * 2, borderRadius: 12 },
    row: { flexDirection: 'row', alignItems: 'center', gap: 8 },
    between: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: 8 },
    divider: { height: StyleSheet.hairlineWidth * 2, backgroundColor: c.line },
    input: { minHeight: 48, borderWidth: 1, borderColor: c.line, borderRadius: 10, backgroundColor: c.panel, paddingHorizontal: 14, color: c.ink, fontSize: 16 },
    fieldLabel: { fontSize: 13, fontWeight: '600', color: c.ink, marginBottom: 6 },
  })
}

interface ThemeCtx { c: Palette; s: ReturnType<typeof makeStyles>; scheme: 'light' | 'dark'; choice: ThemeChoice; setChoice: (t: ThemeChoice) => void }
const Ctx = createContext<ThemeCtx | null>(null)
const styles = { light: makeStyles(light), dark: makeStyles(dark) }

/** First launch follows the phone setting; an explicit choice is remembered (same as the web toggle). */
export function ThemeProvider({ children }: { children: ReactNode }) {
  const system = useColorScheme()
  const [choice, setChoiceState] = useState<ThemeChoice>('system')
  useEffect(() => { void cacheStore.get(KEY).then(v => { if (v === 'light' || v === 'dark') setChoiceState(v) }).catch(() => {}) }, [])
  const scheme = choice === 'system' ? (system === 'dark' ? 'dark' : 'light') : choice
  const value = useMemo<ThemeCtx>(() => ({
    c: scheme === 'dark' ? dark : light, s: styles[scheme], scheme, choice,
    setChoice: t => { setChoiceState(t); void cacheStore.set(KEY, t).catch(() => {}) },
  }), [scheme, choice])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}
export function useTheme() { const v = useContext(Ctx); if (!v) throw new Error('no ThemeProvider'); return v }
