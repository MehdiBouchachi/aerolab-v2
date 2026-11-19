// AeroLab V2 typography: Sora for headings and the wordmark, Inter for everything else.
// Fonts ship inside the app (SIL OFL 1.1, from @expo-google-fonts), so they work offline.
//
// React Native does not inherit fonts, and on Android a custom family ignores fontWeight, so every
// weight is its own file. `Text` below resolves (family, weight) -> the right file, which lets the
// screens keep writing plain `fontWeight: '700'`. Headings opt in with `fontFamily: HEADING`.
import { StyleSheet, Text as RNText, TextInput as RNTextInput, type TextInputProps, type TextProps, type TextStyle } from 'react-native'
import { Inter_400Regular } from '@expo-google-fonts/inter/400Regular'
import { Inter_500Medium } from '@expo-google-fonts/inter/500Medium'
import { Inter_600SemiBold } from '@expo-google-fonts/inter/600SemiBold'
import { Inter_700Bold } from '@expo-google-fonts/inter/700Bold'
import { Inter_800ExtraBold } from '@expo-google-fonts/inter/800ExtraBold'
import { Sora_400Regular } from '@expo-google-fonts/sora/400Regular'
import { Sora_500Medium } from '@expo-google-fonts/sora/500Medium'
import { Sora_600SemiBold } from '@expo-google-fonts/sora/600SemiBold'
import { Sora_700Bold } from '@expo-google-fonts/sora/700Bold'
import { HEADING, resolveFont } from './fontmap'
import { useLanguage, useTranslation } from './language'

export { HEADING }

export const FONT_ASSETS = { Inter_400Regular, Inter_500Medium, Inter_600SemiBold, Inter_700Bold, Inter_800ExtraBold, Sora_400Regular, Sora_500Medium, Sora_600SemiBold, Sora_700Bold }

// Set once the font files are registered. Until then (or if loading failed) text keeps the system
// font instead of naming a family the OS does not know.
let ready = false
export function setFontsReady(v: boolean) { ready = v }

function brandStyle(style: TextProps['style']) {
  const flat = (StyleSheet.flatten(style) ?? {}) as TextStyle
  if (!ready) return flat.fontFamily === HEADING ? [style, { fontFamily: undefined }] : style
  const file = resolveFont(flat.fontFamily, flat.fontWeight)
  return file ? [style, { fontFamily: file, fontWeight: undefined }] : style
}

export function Text({ children, style, accessibilityLabel, ...props }: TextProps) {
  const t = useTranslation()
  const { language } = useLanguage()
  const translateText = (value: unknown) => typeof value === 'string' ? value.replace(value.trim(), t(value.trim())) : value
  const content = Array.isArray(children) ? children.map(translateText) : typeof children === 'string' ? t(children) : children
  return <RNText {...props} accessibilityLabel={accessibilityLabel && t(accessibilityLabel)}
    style={language === 'ar' ? [style, { fontFamily: undefined }] : brandStyle(style)}>{content as TextProps['children']}</RNText>
}
export function TextInput({ style, placeholder, accessibilityLabel, ...props }: TextInputProps) {
  const t = useTranslation()
  const { language } = useLanguage()
  return <RNTextInput {...props} placeholder={placeholder && t(placeholder)} accessibilityLabel={accessibilityLabel && t(accessibilityLabel)}
    style={language === 'ar' ? [style, { fontFamily: undefined }] : brandStyle(style)} />
}
