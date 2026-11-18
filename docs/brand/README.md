# AeroLab V2 identity

Source: the *AeroLab V2 Universal App Assets* package (web, mobile, desktop). The logos are vector
reconstructions of an approved AI-rendered identity board, not a layered original; the package's
licence note says no trademark registration should be assumed. Masters kept here: `brand.json`
(tokens) and `logos/*.svg`. Desktop icon sets (Windows .ico, macOS .icns, Linux, Electron, Tauri) and
the identity board image are in the package and not committed, since there is no desktop app yet.

## Colours
| Token | Value | Use |
|---|---|---|
| Background dark | `#071B16` | dark theme surface, PWA/Android icon background |
| Slate (dark charcoal) | `#0F172A` | light theme text |
| Growth green | `#22C55E` | accent (logo leaf, active marker); dark-theme actions |
| Deep green | `#059F6E` | focus ring (light theme) |
| Mint | `#A7F3D0` | focus ring (dark theme), text selection |
| Background light | `#F7FAF8` | light theme surface |

**Contrast rule (measured, WCAG):** growth green on white is 2.3:1 and deep green 3.4:1, both below
the 4.5:1 needed for text. So in the light theme, text, links and buttons use `#047857`, a darker
shade of the deep green (5.5:1 on white). In the dark theme `#22C55E` on `#071B16` is 7.8:1 and is
the action colour. Amber (attention) and red (critical) keep their meaning and never use brand green.

## Type
Sora for headings and the wordmark; Inter for the interface. Both SIL OFL 1.1 and **bundled** (the
Pi's dashboard must work with no internet):
- Web: `@fontsource-variable/sora` + `/inter`, Latin + Latin Extended only, declared in
  `apps/web/src/styles/brand.css`; licences served from `/licenses`.
- Mobile: `@expo-google-fonts/sora` + `/inter` loaded with `expo-font`; `apps/mobile/src/ui/fonts.tsx`
  maps each weight to its own file (Android ignores `fontWeight` on custom fonts).

## Where things live
| | Web (`apps/web`) | Mobile (`apps/mobile`) |
|---|---|---|
| Tokens | `src/styles/brand.css` (loaded last) | `src/ui/theme.tsx` |
| Logo | `public/branding/*.svg`, `src/components/Logo.tsx` | `assets/branding/*.png`, `src/ui/Logo.tsx` (mark as SVG) |
| Icons | `public/favicon.*`, `public/icons/*`, `public/manifest.json` | `assets/icon.png`, adaptive icon, favicon, splash image |

Logo placement: the full horizontal logo (with V2 and "Smart aeroponics systems") on sign-in and
welcome screens; the mark + AEROLAB wordmark in the sidebar and top bar, where the tagline would be
too small to read. Use `on-light` artwork on light surfaces and `on-dark` on dark ones; both apps
switch automatically with the theme.
