let requestOwner = null
export function setRequestOwner(id) { requestOwner = id }
export async function request(path, options = {}) {
  const { responseType, expectedUser = requestOwner, ...fetchOptions } = options
  const personal = !path.startsWith('/api/admin/') && !['/api/auth/login','/api/auth/register','/api/auth/me'].includes(path)
  const identityHeader = personal && expectedUser != null ? { 'X-GK-Expected-User': String(expectedUser) } : {}
  const res = await fetch(path, {
    credentials: 'same-origin',
    ...fetchOptions,
    headers: { 'Content-Type': 'application/json', ...identityHeader, ...options.headers },
  })
  if (res.ok && responseType === 'blob') return res.blob()
  const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }))
  if (!res.ok) {
    const e = new Error(body.error || `HTTP ${res.status}`)
    e.status = res.status
    e.code = body.code
    if (body.code === 'account_changed') window.dispatchEvent(new CustomEvent('account-changed', { detail: expectedUser }))
    if (res.status === 401 && !path.endsWith('/login') && !path.endsWith('/me')) {
      window.dispatchEvent(new CustomEvent('session-expired', { detail: path.startsWith('/api/admin/') ? 'admin' : { kind: 'user', expectedUser } }))
    }
    throw e
  }
  return body
}
export function query(values = {}) {
  return new URLSearchParams(Object.entries(values).filter(([, v]) => v !== '' && v != null)).toString()
}
export function forUser(owner) {
  // Freeze the issuing page owner; a later setUser cannot rebind this client.
  const scopedRequest = (path, options = {}) => request(path, { ...options, expectedUser: owner })
  return createAPI(scopedRequest)
}
function createAPI(request) {
const post = (path, data = {}) => request(path, { method: 'POST', body: JSON.stringify(data) })
return {
  me: () => request('/api/admin/me'),
  login: (username, password) => post('/api/admin/login', { username, password }),
  logout: () => post('/api/admin/logout'),
  getSettings: () => request('/api/admin/settings'),
  saveSettings: settings => request('/api/admin/settings', { method: 'PUT', body: JSON.stringify(settings) }),
  testLLM: () => post('/api/admin/test-llm'),
  changePassword: (current_password, password) => post('/api/admin/password', { current_password, password }),
  refreshConcepts: () => post('/api/admin/concepts/refresh'),
  userMe: () => request('/api/auth/me'),
  userLogin: (username, password) => post('/api/auth/login', { username, password }),
  register: data => post('/api/auth/register', data),
  userLogout: () => post('/api/auth/logout'),
  userPassword: data => post('/api/auth/password', data),
  profile: () => request('/api/account/profile'),
  saveProfile: data => request('/api/account/profile', { method: 'PUT', body: JSON.stringify(data) }),
  dashboard: days => request(`/api/account/dashboard?${query({ days })}`),
  loginSessions: page => request(`/api/account/sessions?${query({ page })}`),
  revokeSession: id => request(`/api/account/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  revokeOtherSessions: () => post('/api/account/sessions/revoke-others'),
  prioritizedWrongbook: () => post('/api/account/review/wrongbook'),
  exportRecords: days => request(`/api/account/export?${query({ days })}`, { responseType: 'blob' }),
  filters: () => request('/api/filters'),
  questions: f => request(`/api/questions?${query(f)}`),
  papers: f => request(`/api/papers?${query(f)}`),
  concepts: f => request(`/api/concepts?${query(f)}`),
  concept: id => request(`/api/concepts/${id}`),
  createSession: (kind, spec) => {
    const values = Object.fromEntries(Object.entries(spec).filter(([,v]) => v !== '' && v != null))
    for (const key of ['year','paper_id','concept_id','limit','page','size']) {
      if (values[key] != null) values[key] = Number(values[key])
    }
    return post('/api/practice/sessions', { kind, spec: values })
  },
  session: id => request(`/api/practice/sessions/${id}`),
  draft: (id, answers, draft_revision) => request(`/api/practice/sessions/${id}/answers`, { method: 'PUT', body: JSON.stringify({ answers, draft_revision }) }),
  submit: (id, answers, draft_revision) => post(`/api/practice/sessions/${id}/submit`, { answers, draft_revision }),
  records: f => request(`/api/practice/records?${query(f)}`),
  stats: () => request('/api/practice/stats'),
  rebuildStats: () => post('/api/practice/stats/rebuild'),
  wrongbook: f => request(`/api/wrongbook?${query(f)}`),
  wrongAction: (id, action) => post(`/api/wrongbook/${id}`, { action }),
  favorites: f => request(`/api/favorites?${query(f)}`),
  favoriteAction: (id, action) => post(`/api/favorites/${id}`, { action }),
}

}
export const api = createAPI(request)
