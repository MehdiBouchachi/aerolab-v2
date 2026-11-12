import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from '../api/client'
import { useApp } from './context'

export interface Fetched<T> { data: T | null; error: ApiError | null; loading: boolean; fetchedAt: number | null; reload: () => void }

/**
 * GET a path and keep it fresh: refetch when a matching live event arrives
 * (debounced), when the stream (re)connects, and on a slow safety interval.
 * Errors keep the last good data visible and flagged - never silently blank.
 */
export function useFetch<T>(path: string | null, eventTypes: string[] = [], pollMs = 30000): Fetched<T> {
  const { subscribe } = useApp()
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<ApiError | null>(null)
  const [loading, setLoading] = useState(path !== null)
  const [fetchedAt, setAt] = useState<number | null>(null)
  const seq = useRef(0)
  const timer = useRef<number | undefined>(undefined)

  const load = useCallback(() => {
    if (!path) return
    const my = ++seq.current
    api<T>('GET', path).then(d => { if (my === seq.current) { setData(d); setError(null); setAt(Date.now()) } })
      .catch(e => { if (my === seq.current) setError(e as ApiError) })
      .finally(() => { if (my === seq.current) setLoading(false) })
  }, [path])

  useEffect(() => { setData(null); setLoading(path !== null); load() }, [path, load])
  useEffect(() => { if (!pollMs) return; const t = window.setInterval(load, pollMs); return () => window.clearInterval(t) }, [load, pollMs])
  useEffect(() => subscribe(e => {
    if (e.type === 'hello' || eventTypes.includes(e.type)) {
      window.clearTimeout(timer.current); timer.current = window.setTimeout(load, 300)
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [subscribe, load, eventTypes.join(',')])
  return { data, error, loading, fetchedAt, reload: load }
}
