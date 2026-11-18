// AeroLab V2 identity artwork (brand assets package). The mark is embedded as SVG so it stays sharp
// at any size; the full logo (with V2 and "Smart aeroponics systems") is a transparent PNG.
import { Image, View } from 'react-native'
import { SvgXml } from 'react-native-svg'
import { HEADING, Text } from './fonts'
import { useTheme } from './theme'

const MARK_ON_LIGHT = '<svg xmlns="http://www.w3.org/2000/svg" width="760" height="480" viewBox="0 0 760 480"><defs><linearGradient id="aLight" x1="0.08" y1="0.05" x2="0.92" y2="1"><stop offset="0" stop-color="#FFFFFF"/><stop offset="0.6" stop-color="#E7EDEF"/><stop offset="1" stop-color="#B7C2C8"/></linearGradient><linearGradient id="aDark" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#17252F"/><stop offset="1" stop-color="#071713"/></linearGradient><linearGradient id="leaf" x1="0" y1="1" x2="1" y2="0"><stop offset="0" stop-color="#168B5A"/><stop offset="0.55" stop-color="#55BB54"/><stop offset="1" stop-color="#A4D744"/></linearGradient></defs><g transform="translate(29 22)"><g id="letter-A" fill="url(#aDark)"><path d="M 14,421 L 211,91 C 242,36 283,5 330,2 C 385,-4 425,29 453,73 L 600,290 C 613,305 631,310 645,310 L 569,296 C 540,291 518,281 502,262 L 404,112 C 388,83 363,67 344,74 C 324,76 310,92 294,118 L 170,336 C 133,393 103,421 61,421 Z"/><path d="M 525,316 C 562,324 586,327 627,329 L 692,421 L 640,421 C 597,421 569,402 551,368 Z"/></g><g id="leaf"><path d="M 180,381 C 230,290 327,227 453,253 C 394,336 317,394 180,381 Z" fill="url(#leaf)"/><path d="M 190,374 C 270,331 330,286 437,259" stroke="#A7F3D0" stroke-width="3" stroke-linecap="round" fill="none"/></g></g></svg>'
const MARK_ON_DARK = '<svg xmlns="http://www.w3.org/2000/svg" width="760" height="480" viewBox="0 0 760 480"><defs><linearGradient id="aLight" x1="0.08" y1="0.05" x2="0.92" y2="1"><stop offset="0" stop-color="#FFFFFF"/><stop offset="0.6" stop-color="#E7EDEF"/><stop offset="1" stop-color="#B7C2C8"/></linearGradient><linearGradient id="aDark" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#17252F"/><stop offset="1" stop-color="#071713"/></linearGradient><linearGradient id="leaf" x1="0" y1="1" x2="1" y2="0"><stop offset="0" stop-color="#168B5A"/><stop offset="0.55" stop-color="#55BB54"/><stop offset="1" stop-color="#A4D744"/></linearGradient></defs><g transform="translate(29 22)"><g id="letter-A" fill="url(#aLight)"><path d="M 14,421 L 211,91 C 242,36 283,5 330,2 C 385,-4 425,29 453,73 L 600,290 C 613,305 631,310 645,310 L 569,296 C 540,291 518,281 502,262 L 404,112 C 388,83 363,67 344,74 C 324,76 310,92 294,118 L 170,336 C 133,393 103,421 61,421 Z"/><path d="M 525,316 C 562,324 586,327 627,329 L 692,421 L 640,421 C 597,421 569,402 551,368 Z"/></g><g id="leaf"><path d="M 180,381 C 230,290 327,227 453,253 C 394,336 317,394 180,381 Z" fill="url(#leaf)"/><path d="M 190,374 C 270,331 330,286 437,259" stroke="#A7F3D0" stroke-width="3" stroke-linecap="round" fill="none"/></g></g></svg>'
const LOGO_ON_LIGHT = require('../../assets/branding/logo-on-light.png')
const LOGO_ON_DARK = require('../../assets/branding/logo-on-dark.png')

/** A + leaf mark, in the variant for the current theme. Decorative: pair it with text. */
export function Mark({ size = 28 }: { size?: number }) {
  const { scheme } = useTheme()
  return <SvgXml xml={scheme === 'dark' ? MARK_ON_DARK : MARK_ON_LIGHT} width={size} height={size * 480 / 760} />
}

/** Mark + AEROLAB wordmark for the top bar (same lockup as the web sidebar). */
export function BrandLockup() {
  const { c } = useTheme()
  return <View style={{ flexDirection: 'row', alignItems: 'center', gap: 8 }} accessible accessibilityRole="header" accessibilityLabel="AeroLab">
    <Mark size={30} />
    <Text style={{ fontFamily: HEADING, fontWeight: '500', fontSize: 15, letterSpacing: 4.5, color: c.ink }}>AEROLAB</Text>
    <Text style={{ fontWeight: '600', fontSize: 9, color: c.accent, marginLeft: -4, marginTop: -8 }}>V2</Text>
  </View>
}

/** Full horizontal logo for the welcome and sign-in screens. */
export function FullLogo({ width = 260 }: { width?: number }) {
  const { scheme } = useTheme()
  return <Image source={scheme === 'dark' ? LOGO_ON_DARK : LOGO_ON_LIGHT} style={{ width, height: width * 289 / 1100, alignSelf: 'center' }}
    accessibilityRole="image" accessibilityLabel="AeroLab V2, smart aeroponics systems" />
}
