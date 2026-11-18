import { describe, expect, it } from 'vitest'
import { HEADING, resolveFont } from './fontmap'

describe('brand font mapping', () => {
  it('uses Inter for interface text, one file per weight (Android ignores fontWeight on custom fonts)', () => {
    expect(resolveFont(undefined, undefined)).toBe('Inter_400Regular')
    expect(resolveFont(undefined, '600')).toBe('Inter_600SemiBold')
    expect(resolveFont(undefined, 'bold')).toBe('Inter_700Bold')
    expect(resolveFont(undefined, '800')).toBe('Inter_800ExtraBold')
  })
  it('uses Sora for headings, capping at the heaviest bundled weight', () => {
    expect(resolveFont(HEADING, '600')).toBe('Sora_600SemiBold')
    expect(resolveFont(HEADING, '800')).toBe('Sora_700Bold')
  })
  it('clamps light weights to regular (no thin files are bundled) and leaves explicit families alone', () => {
    expect(resolveFont(undefined, '300')).toBe('Inter_400Regular')
    expect(resolveFont('monospace', '700')).toBeNull()
  })
})
