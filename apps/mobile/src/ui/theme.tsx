// Keep these roles aligned with apps/web/src/styles/brand.css and dashboard.css.
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { StyleSheet, useColorScheme } from 'react-native'
import { cacheStore } from '../native'
import { HEADING } from './fonts'

export const light = {
  bg: '#F8FAFC', panel: '#FFFFFF', ink: '#0F172A', muted: '#475569', line: '#E2E8F0',
  brand: '#047857', brandSoft: '#E3F5EC', buttonFill: '#047857', buttonHover: '#065F46', onBrand: '#FFFFFF', accent: '#22C55E',
  attention: '#a76412', attentionSoft: '#fff4e5', danger: '#b23b44', dangerSoft: '#fcecee',
  segTrack: '#E2E8F0', segActive: '#ECECEF', backdrop: 'rgba(0,0,0,0.4)',
}
export type Palette = typeof light
export const dark: Palette = {
  bg: '#262626', panel: '#2F2F2F', ink: '#F5F5F5', muted: '#D4D4D4', line: '#404040',
  brand: '#86C5A3', brandSoft: '#064E3B', buttonFill: '#107348', buttonHover: '#0D6540', onBrand: '#FFFFFF', accent: '#22C55E',
  attention: '#f5c47d', attentionSoft: '#3d3024', danger: '#ffa3a9', dangerSoft: '#422b33',
  segTrack: '#404040', segActive: '#3F3F3F', backdrop: 'rgba(0,0,0,0.4)',
}

export type ThemeChoice = 'system' | 'light' | 'dark'
const KEY = 'theme'

function makeStyles(c: Palette) {
  return StyleSheet.create({
    screen: { flex: 1, backgroundColor: c.bg },
    pad: { paddingHorizontal: 16, paddingTop: 14, paddingBottom: 28, gap: 12 },
    h1: { fontFamily: HEADING, fontSize: 22, lineHeight: 28, fontWeight: '700', letterSpacing: -0.5, color: c.ink },
    h2: { fontFamily: HEADING, fontSize: 16, fontWeight: '600', letterSpacing: -0.2, color: c.ink },
    h3: { fontFamily: HEADING, fontSize: 14, fontWeight: '600', color: c.ink },
    text: { fontSize: 14, color: c.ink },
    strong: { fontSize: 14, fontWeight: '700', color: c.ink },
    muted: { fontSize: 12.5, color: c.muted },
    small: { fontSize: 11.5, color: c.muted },
    panel: { backgroundColor: c.panel, borderColor: c.line, borderWidth: StyleSheet.hairlineWidth * 2, borderRadius: 12 },
    row: { flexDirection: 'row', alignItems: 'center', gap: 8 },
    between: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: 8 },
    divider: { height: StyleSheet.hairlineWidth * 2, backgroundColor: c.line },
    input: { minHeight: 48, borderWidth: 1, borderColor: c.line, borderRadius: 10, backgroundColor: c.panel, paddingHorizontal: 14, color: c.ink, fontSize: 14 },
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
