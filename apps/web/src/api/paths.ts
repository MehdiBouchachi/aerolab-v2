// One dashboard build, served unchanged by both the Pi and the cloud. At start-up it asks the server that
// loaded it which one it is (GET /api/v1/meta) instead of being compiled twice. The bindings below are
// live: modules that import them see the detected values, as long as they read them at render time.

export type Source = 'local' | 'cloud'

export let SOURCE: Source = 'local'
export let IS_CLOUD = false
/** The selected site's overview: the cloud's home page is the fleet of every Pi. */
export let HOME = '/'

const build = (cloud: boolean) => ({
  login: cloud ? '/api/v1/auth/login' : '/api/v1/local/auth/login',
  logout: cloud ? '/api/v1/auth/logout' : '/api/v1/local/auth/logout',
  session: (site?: string) => '/api/v1/session' + (cloud && site ? `?site=${encodeURIComponent(site)}` : ''),
  unit: (site: string, id: string) => cloud ? `/api/v1/sites/${site}/units/${id}` : `/api/v1/units/${id}`,
  history: (site: string, id: string) => cloud ? `/api/v1/sites/${site}/units/${id}/readings/history` : `/api/v1/units/${id}/readings/history`,
  applied: (site: string, id: string) => cloud ? `/api/v1/sites/${site}/units/${id}/programs/applied` : `/api/v1/units/${id}/programs/applied`,
  programRequest: (site: string, id: string) => cloud ? `/api/v1/sites/${site}/remote-program-requests` : `/api/v1/units/${id}/program-requests`,
  command: (site: string, id: string) => cloud ? `/api/v1/sites/${site}/remote-commands/${id}` : `/api/v1/commands/${id}`,
  /**
   * A feature the Pi owns (crop profiles, adding/renaming units, alert acknowledgement, storage...).
   * On the Pi it is called directly; from the cloud it goes through the relay, and the Pi runs it with
   * the same handler and the member's role. `path` is the Pi's own API path.
   */
  site: (site: string, path: string) => cloud ? `/api/v1/sites/${site}/edge/${path.replace(/^\/api\/v1\//, '')}` : path,
})

export let P = build(false)

export function setSource(s: Source) {
  SOURCE = s
  IS_CLOUD = s === 'cloud'
  HOME = IS_CLOUD ? '/site' : '/'
  P = build(IS_CLOUD)
}

/** Ask the server that served this page what it is. Falls back to the dev-server setting. */
export async function detectSource(fetcher: typeof fetch = fetch): Promise<Source> {
  let s: Source = import.meta.env.VITE_SOURCE === 'cloud' ? 'cloud' : 'local'
  try {
    const r = await fetcher('/api/v1/meta', { headers: { Accept: 'application/json' } })
    if (r.ok) {
      const j = await r.json() as { origin?: string }
      if (j.origin === 'cloud') s = 'cloud'
      else if (j.origin === 'edge') s = 'local'
    }
  } catch { /* offline dev server: keep the fallback */ }
  setSource(s)
  return s
}
