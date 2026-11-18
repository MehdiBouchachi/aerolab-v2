// Pure (family, weight) -> bundled font file mapping, kept free of React Native so it is unit-tested.
/** Marker family for headings; resolved to a Sora file. */
export const HEADING = 'AeroLabHeading'

type Weight = string | number | undefined

const INTER: Record<number, string> = { 400: 'Inter_400Regular', 500: 'Inter_500Medium', 600: 'Inter_600SemiBold', 700: 'Inter_700Bold', 800: 'Inter_800ExtraBold' }
const SORA: Record<number, string> = { 400: 'Sora_400Regular', 500: 'Sora_500Medium', 600: 'Sora_600SemiBold', 700: 'Sora_700Bold', 800: 'Sora_700Bold' }

function weightOf(w: Weight): number {
  if (w === 'bold') return 700
  if (w === undefined || w === 'normal') return 400
  const n = Number(w)
  return Number.isFinite(n) ? n : 400
}

/** Which bundled font file a (family, weight) style uses; null leaves an explicit family alone. */
export function resolveFont(family: string | undefined, weight: Weight): string | null {
  if (family && family !== HEADING) return null
  const table = family === HEADING ? SORA : INTER
  const w = Math.min(800, Math.max(400, Math.round(weightOf(weight) / 100) * 100))
  return table[w] ?? table[400]
}
