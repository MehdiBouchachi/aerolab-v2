import type { ReactNode } from 'react'
import { ActivityIndicator, Pressable, RefreshControl, ScrollView, View, type StyleProp, type ViewStyle } from 'react-native'
import { Text } from './fonts'
import { BrandLockup } from './Logo'
import { useSafeAreaInsets } from 'react-native-safe-area-context'
import { Icon, type IconName } from './Icon'
import { useTheme, type Palette } from './theme'
import type { Tone } from './format'

export const toneColors = (c: Palette, tone: Tone) =>
  tone === 'ok' ? { fg: c.brand, bg: c.brandSoft } : tone === 'warn' ? { fg: c.attention, bg: c.attentionSoft } : tone === 'crit' ? { fg: c.danger, bg: c.dangerSoft } : { fg: c.muted, bg: c.bg }

/** Status badge; the glyph and words carry the meaning, colour only reinforces it (same as the web). */
export function Badge({ tone, children }: { tone: Tone; children: ReactNode }) {
  const { c } = useTheme(); const t = toneColors(c, tone)
  return <View style={{ backgroundColor: t.bg, borderRadius: 6, paddingHorizontal: 7, paddingVertical: 3, flexShrink: 0 }}>
    <Text style={{ color: t.fg, fontSize: 11.5, fontWeight: '600' }}>{tone === 'ok' ? '✔ ' : tone === 'crit' ? '■ ' : tone === 'warn' ? '▲ ' : ''}{children}</Text>
  </View>
}

/** Simulator data is always labelled. */
export function Sim() {
  const { c } = useTheme()
  return <View accessible accessibilityLabel="Simulated data, not real hardware" style={{ borderWidth: 1, borderColor: c.line, backgroundColor: c.bg, borderRadius: 4, paddingHorizontal: 4, paddingVertical: 1 }}>
    <Text style={{ color: c.muted, fontSize: 9.5, fontWeight: '700', letterSpacing: 0.6 }}>SIMULATED</Text>
  </View>
}

type BtnKind = 'primary' | 'secondary' | 'danger' | 'link'
export function Btn({ label, onPress, kind = 'secondary', disabled, icon, style }: { label: string; onPress: () => void; kind?: BtnKind; disabled?: boolean; icon?: IconName; style?: StyleProp<ViewStyle> }) {
  const { c } = useTheme()
  const fg = kind === 'primary' ? c.onBrand : kind === 'danger' ? c.danger : kind === 'link' ? c.brand : c.ink
  return <Pressable accessibilityRole="button" accessibilityLabel={label} accessibilityState={{ disabled: !!disabled }} disabled={disabled} onPress={onPress}
    style={({ pressed }) => [{
      minHeight: kind === 'link' ? 36 : 48, borderRadius: 10, flexDirection: 'row', alignItems: 'center', justifyContent: kind === 'link' ? 'flex-start' : 'center', gap: 8, paddingHorizontal: kind === 'link' ? 0 : 16,
      backgroundColor: kind === 'primary' ? (pressed ? c.buttonHover : c.buttonFill) : kind === 'link' ? 'transparent' : c.panel,
      borderWidth: kind === 'primary' || kind === 'link' ? 0 : 1, borderColor: c.line,
      opacity: disabled ? 0.55 : pressed ? 0.8 : 1,
    }, style]}>
    {icon && <Icon name={icon} size={18} color={fg} />}
    <Text style={{ color: fg, fontSize: 15, fontWeight: kind === 'primary' ? '700' : '600' }}>{label}</Text>
  </Pressable>
}

/** Section navigation shares the web dashboard's neutral selection pill. */
export function Segmented<T extends string>({ value, options, onChange, label }: { value: T; options: [T, string][]; onChange: (v: T) => void; label: string }) {
  const { c } = useTheme()
  return <View accessibilityRole="tablist" accessibilityLabel={label} style={{ flexDirection: 'row', flexWrap: 'wrap', gap: 4 }}>
    {options.map(([v, l]) => {
      const on = v === value
      return <Pressable key={v} accessibilityRole="tab" accessibilityState={{ selected: on }} onPress={() => onChange(v)}
        style={({ pressed }) => ({ minHeight: 44, paddingHorizontal: 12, borderRadius: 999, alignItems: 'center', justifyContent: 'center', backgroundColor: on || pressed ? c.segActive : 'transparent' })}>
        <Text style={{ fontSize: 13.5, fontWeight: '600', color: on ? c.ink : c.muted }}>{l}</Text>
      </Pressable>
    })}
  </View>
}

export function Banner({ tone, title, children }: { tone: Tone; title?: string; children?: ReactNode }) {
  const { c, s } = useTheme(); const t = toneColors(c, tone)
  return <View accessibilityRole="alert" style={{ backgroundColor: t.bg, borderRadius: 10, borderLeftWidth: 3, borderLeftColor: t.fg, padding: 12, gap: 3 }}>
    {title && <Text style={[s.strong, { color: tone === 'muted' ? c.ink : t.fg, fontSize: 14 }]}>{title}</Text>}
    {typeof children === 'string' ? <Text style={[s.text, { fontSize: 13.5 }]}>{children}</Text> : children}
  </View>
}

export function Screen({ children, onRefresh, refreshing }: { children: ReactNode; onRefresh?: () => void; refreshing?: boolean }) {
  const { s } = useTheme()
  return <ScrollView style={s.screen} contentContainerStyle={s.pad} keyboardShouldPersistTaps="handled"
    refreshControl={onRefresh ? <RefreshControl refreshing={!!refreshing} onRefresh={onRefresh} /> : undefined}>{children}</ScrollView>
}

export function PageTitle({ title, subtitle, right }: { title: string; subtitle?: string; right?: ReactNode }) {
  const { s } = useTheme()
  return <View style={[s.between, { alignItems: 'flex-end', marginBottom: 2 }]}>
    <View style={{ flexShrink: 1 }}><Text accessibilityRole="header" style={s.h1}>{title}</Text>{subtitle ? <Text style={[s.muted, { marginTop: 2 }]}>{subtitle}</Text> : null}</View>
    {right}
  </View>
}

export function SectionHeading({ title, right }: { title: string; right?: string }) {
  const { s } = useTheme()
  return <View style={[s.between, { marginTop: 10 }]}><Text accessibilityRole="header" style={s.h2}>{title}</Text>{right ? <Text style={s.muted}>{right}</Text> : null}</View>
}

export function EmptyState({ icon, title, children }: { icon: IconName; title: string; children: string }) {
  const { c, s } = useTheme()
  return <View style={[s.panel, { padding: 24, alignItems: 'center', gap: 8 }]}>
    <View style={{ width: 44, height: 44, borderRadius: 22, backgroundColor: c.bg, alignItems: 'center', justifyContent: 'center' }}><Icon name={icon} size={21} color={c.muted} /></View>
    <Text style={[s.h3, { textAlign: 'center' }]}>{title}</Text><Text style={[s.muted, { textAlign: 'center' }]}>{children}</Text>
  </View>
}

export const Loading = ({ what }: { what: string }) => {
  const { c, s } = useTheme()
  return <View style={[s.row, { paddingVertical: 8 }]}><ActivityIndicator color={c.brand} /><Text style={s.muted}>Loading {what}…</Text></View>
}

/** Sticky top bar: [back] brand [account]. Matches the web's phone header. */
export function TopBar({ onBack, onAccount, accountActive }: { onBack?: () => void; onAccount: () => void; accountActive: boolean }) {
  const { c } = useTheme(); const inset = useSafeAreaInsets()
  return <View style={{ paddingTop: inset.top, backgroundColor: c.panel, borderBottomWidth: 1, borderBottomColor: c.line }}>
    <View style={{ height: 52, flexDirection: 'row', alignItems: 'center', paddingHorizontal: onBack ? 4 : 16 }}>
      {onBack && <Pressable accessibilityRole="button" accessibilityLabel="Back" onPress={onBack} hitSlop={6} style={({ pressed }) => ({ width: 44, height: 44, borderRadius: 10, alignItems: 'center', justifyContent: 'center', backgroundColor: pressed ? c.brandSoft : 'transparent' })}><Icon name="back" size={22} color={c.brand} /></Pressable>}
      <View style={{ flex: 1, flexDirection: 'row', alignItems: 'center' }}>
        <BrandLockup />
      </View>
      <Pressable accessibilityRole="button" accessibilityLabel="Account and settings" onPress={onAccount} hitSlop={6}
        style={({ pressed }) => ({ width: 44, height: 44, marginRight: onBack ? 8 : -8, borderRadius: 10, alignItems: 'center', justifyContent: 'center', backgroundColor: pressed || accountActive ? c.brandSoft : 'transparent' })}>
        <Icon name="admin" size={20} color={accountActive ? c.brand : c.ink} />
      </Pressable>
    </View>
  </View>
}

/** One-line status strip under the top bar: source, freshness, SIMULATED. Never hides that data is cached. */
export function StatusStrip({ items, simulated }: { items: { icon: IconName; text: string; tone?: Tone }[]; simulated?: boolean }) {
  const { c } = useTheme()
  return <View accessibilityRole="summary" style={{ flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', gap: 6, columnGap: 14, paddingHorizontal: 16, paddingVertical: 7, backgroundColor: c.panel, borderBottomWidth: 1, borderBottomColor: c.line }}>
    {items.map((it, i) => {
      const fg = it.tone === 'warn' ? c.attention : it.tone === 'crit' ? c.danger : c.ink
      return <View key={i} style={{ flexDirection: 'row', alignItems: 'center', gap: 5, flexShrink: 1 }}>
        <Icon name={it.icon} size={14} color={it.tone && it.tone !== 'ok' ? fg : c.brand} />
        <Text style={{ fontSize: 12, fontWeight: '700', color: fg, flexShrink: 1 }}>{it.text}</Text>
      </View>
    })}
    {simulated && <View style={{ marginLeft: 'auto' }}><Sim /></View>}
  </View>
}

export type Tab = 'overview' | 'alerts' | 'account'
/** Bottom tab bar, same three destinations and treatment as the web on phones. */
export function TabBar({ tab, onTab, alertCount }: { tab: Tab | null; onTab: (t: Tab) => void; alertCount: number }) {
  const { c } = useTheme(); const inset = useSafeAreaInsets()
  const items: [Tab, IconName, string][] = [['overview', 'overview', 'Overview'], ['alerts', 'alerts', 'Alerts'], ['account', 'admin', 'Account']]
  return <View accessibilityRole="tablist" style={{ flexDirection: 'row', paddingBottom: Math.max(inset.bottom, 6), paddingTop: 4, paddingHorizontal: 6, backgroundColor: c.panel, borderTopWidth: 1, borderTopColor: c.line }}>
    {items.map(([t, icon, l]) => {
      const on = tab === t
      return <Pressable key={t} accessibilityRole="tab" accessibilityLabel={t === 'alerts' && alertCount ? `${l}, ${alertCount} active` : l} accessibilityState={{ selected: on }} onPress={() => onTab(t)}
        style={({ pressed }) => ({ flex: 1, minHeight: 52, alignItems: 'center', justifyContent: 'center', gap: 2, opacity: pressed ? 0.7 : 1 })}>
        <View style={{ minWidth: 48, paddingVertical: 3, alignItems: 'center', borderRadius: 999, backgroundColor: on ? c.segActive : 'transparent' }}><Icon name={icon} size={22} color={on ? c.ink : c.muted} />
          {t === 'alerts' && alertCount > 0 && <View style={{ position: 'absolute', top: -4, left: 14, minWidth: 17, height: 17, borderRadius: 9, paddingHorizontal: 4, backgroundColor: c.attentionSoft, alignItems: 'center', justifyContent: 'center' }}><Text style={{ fontSize: 10, fontWeight: '800', color: c.attention }}>{alertCount}</Text></View>}
        </View>
        <Text style={{ fontSize: 11, fontWeight: on ? '700' : '500', color: on ? c.ink : c.muted }}>{l}</Text>
      </Pressable>
    })}
  </View>
}
