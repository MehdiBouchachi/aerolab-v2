import { useCallback, useEffect, useState } from 'react'
import { ApiFailure, type Reading } from '../core/session'
import type { Source } from '../core/types'
import { useApp } from './AppState'
import { paths } from './format'

/** The source the current data comes from (live source, or the source of the cached copy). */
export function useSource(): Source {
  const { decision } = useApp()
  if (decision?.kind === 'live') return decision.source
  if (decision?.kind === 'cached' && decision.cachedFrom) return decision.cachedFrom
  return 'local'
}
export function usePaths() {
  const { pairing } = useApp()
  return paths(useSource(), pairing?.siteId ?? '')
}

export function useSiteCapabilities() {
  const { decision } = useApp()
  const { session } = usePaths()
  return useResource<{ capabilities: string[] }>(decision?.kind === 'live' ? session : null)
}

/** Loads one resource from the CURRENT source; falls back to that source's own cache, clearly marked. */
export function useResource<T>(path: string | null, intervalMs = 10000) {
  const { session, decision, pairing, userId } = useApp()
  const connection = decision?.kind
  const scope = JSON.stringify([pairing?.siteId, userId, decision?.kind === 'live' ? decision.source : decision?.kind === 'cached' ? decision.cachedFrom : null,
    decision?.kind === 'live' ? decision.baseUrl : null])
  const [r, setR] = useState<Reading<T> | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const load = useCallback(async () => {
    if (!path || !connection) return
    setLoading(true)
    try {
      if (connection === 'live') setR(await session.get<T>(path))
      else setR(await session.cached<T>(path))
      setErr(null)
    } catch (e) {
      if (e instanceof ApiFailure && e.code === 'SUPERSEDED') return
      setErr(e instanceof ApiFailure ? e.message : 'Failed to load')
      setR(await session.cached<T>(path)) // keep showing the last good copy, flagged as cached
    } finally { setLoading(false) }
  }, [path, connection, scope, session])
  useEffect(() => {
    setR(null); void load()
    if (!intervalMs) return
    const t = setInterval(() => void load(), intervalMs); return () => clearInterval(t)
  }, [load, intervalMs])
  return { r, err, loading, reload: load }
}
