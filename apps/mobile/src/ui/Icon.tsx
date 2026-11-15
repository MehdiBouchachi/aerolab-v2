// Same outline icon set as apps/web/src/components/Icon.tsx (24px grid, 1.8 stroke).
import type { ReactNode } from 'react'
import Svg, { Circle, Path, Rect } from 'react-native-svg'

export type IconName = 'overview' | 'alerts' | 'admin' | 'sun' | 'moon' | 'activity' | 'wifi' | 'clock' | 'warning' | 'critical' | 'arrow' | 'check' | 'back' | 'chevron' | 'cloud' | 'qr' | 'settings'

const paths: Record<IconName, ReactNode> = {
  overview: <><Rect x="3" y="3" width="7" height="7" rx="1" /><Rect x="14" y="3" width="7" height="7" rx="1" /><Rect x="3" y="14" width="7" height="7" rx="1" /><Rect x="14" y="14" width="7" height="7" rx="1" /></>,
  alerts: <><Path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9Z" /><Path d="M10 21h4" /></>,
  admin: <><Circle cx="12" cy="8" r="4" /><Path d="M4 21a8 8 0 0 1 16 0" /></>,
  sun: <><Circle cx="12" cy="12" r="4" /><Path d="M12 2v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></>,
  moon: <Path d="M20.5 14.2A8.5 8.5 0 0 1 9.8 3.5 8.5 8.5 0 1 0 20.5 14.2Z" />,
  activity: <Path d="M2 12h4l3-6 5 12 3-6h5" />,
  wifi: <><Path d="M2 8a16 16 0 0 1 20 0M5 12a11 11 0 0 1 14 0m-11 4a6 6 0 0 1 8 0" /><Circle cx="12" cy="20" r="1" fill="currentColor" stroke="none" /></>,
  clock: <><Circle cx="12" cy="12" r="9" /><Path d="M12 7v5l3 2" /></>,
  warning: <><Path d="m12 3 10 18H2L12 3Z" /><Path d="M12 9v5m0 3h.01" /></>,
  critical: <><Circle cx="12" cy="12" r="9" /><Path d="M12 7v6m0 4h.01" /></>,
  arrow: <Path d="M4 12h16m-6-6 6 6-6 6" />,
  check: <Path d="m4 12 5 5L20 6" />,
  back: <Path d="m15 5-7 7 7 7" />,
  chevron: <Path d="m9 5 7 7-7 7" />,
  cloud: <Path d="M7 18a5 5 0 1 1 .9-9.9A6 6 0 0 1 19 10a4 4 0 0 1-1 8H7Z" />,
  qr: <><Rect x="3" y="3" width="7" height="7" rx="1" /><Rect x="14" y="3" width="7" height="7" rx="1" /><Rect x="3" y="14" width="7" height="7" rx="1" /><Path d="M14 14h3v3h-3zM20 14v.01M14 20h.01M17 20h4v-3" /></>,
  settings: <><Circle cx="12" cy="12" r="3" /><Path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1Z" /></>,
}

export function Icon({ name, size = 18, color }: { name: IconName; size?: number; color: string }) {
  return (
    <Svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} color={color} strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" accessibilityElementsHidden importantForAccessibility="no-hide-descendants">
      {paths[name]}
    </Svg>
  )
}
