// One web app, two origins. The build-time flag picks which API this bundle
// talks to; paths differ only where the cloud needs a site in the URL.
export const SOURCE: 'local' | 'cloud' = import.meta.env.VITE_SOURCE === 'cloud' ? 'cloud' : 'local'
export const IS_CLOUD = SOURCE === 'cloud'

export const P = {
  login: IS_CLOUD ? '/api/v1/auth/login' : '/api/v1/local/auth/login',
  logout: IS_CLOUD ? '/api/v1/auth/logout' : '/api/v1/local/auth/logout',
  session: (site?: string) => '/api/v1/session' + (IS_CLOUD && site ? `?site=${encodeURIComponent(site)}` : ''),
  unit: (site: string, id: string) => IS_CLOUD ? `/api/v1/sites/${site}/units/${id}` : `/api/v1/units/${id}`,
  history: (site: string, id: string) => IS_CLOUD ? `/api/v1/sites/${site}/units/${id}/readings/history` : `/api/v1/units/${id}/readings/history`,
  applied: (site: string, id: string) => IS_CLOUD ? `/api/v1/sites/${site}/units/${id}/programs/applied` : `/api/v1/units/${id}/programs/applied`,
  programRequest: (site: string, id: string) => IS_CLOUD ? `/api/v1/sites/${site}/remote-program-requests` : `/api/v1/units/${id}/program-requests`,
  command: (site: string, id: string) => IS_CLOUD ? `/api/v1/sites/${site}/remote-commands/${id}` : `/api/v1/commands/${id}`,
}
