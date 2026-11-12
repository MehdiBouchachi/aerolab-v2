import { StyleSheet } from 'react-native'
export const c = { bg: '#f6f7f3', panel: '#ffffff', ink: '#1d2a1f', muted: '#5b6b5e', line: '#d9ded5', green: '#2f6b3a', greenBg: '#e6f1e7', warn: '#8a5a00', warnBg: '#fff3d6', crit: '#a1261c', critBg: '#fde7e4' }
export const s = StyleSheet.create({
  screen: { flex: 1, backgroundColor: c.bg }, pad: { padding: 16, gap: 12 },
  h1: { fontSize: 22, fontWeight: '700', color: c.ink }, h2: { fontSize: 16, fontWeight: '700', color: c.ink, marginTop: 8 },
  muted: { color: c.muted, fontSize: 13 }, text: { color: c.ink, fontSize: 15 },
  card: { backgroundColor: c.panel, borderColor: c.line, borderWidth: 1, borderRadius: 8, padding: 12, gap: 4 },
  row: { flexDirection: 'row', justifyContent: 'space-between', gap: 8 },
  tile: { flex: 1, backgroundColor: c.panel, borderColor: c.line, borderWidth: 1, borderRadius: 8, padding: 10, minWidth: 90 },
  btn: { minHeight: 48, borderRadius: 8, borderWidth: 1, borderColor: c.line, backgroundColor: c.panel, alignItems: 'center', justifyContent: 'center', paddingHorizontal: 14 },
  primary: { backgroundColor: c.green, borderColor: c.green }, primaryText: { color: '#fff', fontWeight: '700' },
  input: { minHeight: 48, borderWidth: 1, borderColor: c.line, borderRadius: 8, backgroundColor: c.panel, paddingHorizontal: 12, color: c.ink },
  banner: { padding: 10, borderRadius: 8, borderWidth: 1 },
  warnB: { backgroundColor: c.warnBg, borderColor: c.warn }, critB: { backgroundColor: c.critBg, borderColor: c.crit }, okB: { backgroundColor: c.greenBg, borderColor: c.green },
})
