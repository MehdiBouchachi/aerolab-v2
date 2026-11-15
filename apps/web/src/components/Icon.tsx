import type { ReactNode } from 'react'

type Name = 'overview' | 'alerts' | 'admin' | 'sun' | 'moon' | 'activity' | 'wifi' | 'clock' | 'warning' | 'units' | 'offline' | 'critical' | 'arrow' | 'droplet' | 'beaker' | 'thermometer' | 'check' | 'back' | 'chevron'

const paths: Record<Name, ReactNode> = {
  overview: <><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></>,
  alerts: <><path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9Z"/><path d="M10 21h4"/></>,
  admin: <><circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/></>,
  sun: <><circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></>,
  moon: <path d="M20.5 14.2A8.5 8.5 0 0 1 9.8 3.5 8.5 8.5 0 1 0 20.5 14.2Z"/>,
  activity: <path d="M2 12h4l3-6 5 12 3-6h5"/>,
  wifi: <><path d="M2 8a16 16 0 0 1 20 0M5 12a11 11 0 0 1 14 0m-11 4a6 6 0 0 1 8 0"/><circle cx="12" cy="20" r="1" fill="currentColor" stroke="none"/></>,
  clock: <><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></>,
  warning: <><path d="m12 3 10 18H2L12 3Z"/><path d="M12 9v5m0 3h.01"/></>,
  units: <><rect x="3" y="5" width="18" height="14" rx="2"/><path d="M7 9h10M7 13h6"/></>,
  offline: <><path d="M4 4l16 16M3 9a16 16 0 0 1 4-2m4-1a16 16 0 0 1 10 3M5 13a11 11 0 0 1 5-2m4 0a11 11 0 0 1 5 2m-11 4a6 6 0 0 1 8 0"/></>,
  critical: <><circle cx="12" cy="12" r="9"/><path d="M12 7v6m0 4h.01"/></>,
  arrow: <path d="M4 12h16m-6-6 6 6-6 6"/>,
  droplet: <path d="M12 2C9 7 5 11 5 15a7 7 0 0 0 14 0c0-4-4-8-7-13Z"/>,
  beaker: <><path d="M9 3h6m-5 0v7L5 19a2 2 0 0 0 2 3h10a2 2 0 0 0 2-3l-5-9V3M7 17h10"/></>,
  thermometer: <><path d="M10 14V5a2 2 0 0 1 4 0v9a4 4 0 1 1-4 0Z"/><path d="M12 9v8"/></>,
  check: <path d="m4 12 5 5L20 6"/>,
  back: <path d="m15 5-7 7 7 7"/>,
  chevron: <path d="m9 5 7 7-7 7"/>,
}

export function Icon({ name, size = 18, className = '' }: { name: Name; size?: number; className?: string }) {
  return <svg className={className} width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[name]}</svg>
}
