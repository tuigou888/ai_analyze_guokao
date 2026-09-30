export async function request(path, options = {}) {
  const res = await fetch(path, {
    credentials: 'same-origin',
    ...options,
    headers: { 'Content-Type': 'application/json', ...options.headers },
  })
  const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }))
  if (!res.ok) {
    const e = new Error(body.error || `HTTP ${res.status}`)
    e.status = res.status
    if (res.status === 401 && !path.endsWith('/login') && !path.endsWith('/me')) {
      window.dispatchEvent(new CustomEvent('session-expired', { detail: path.startsWith('/api/admin/') ? 'admin' : 'user' }))
    }
    throw e
  }
  return body
}
const post = (path, data = {}) => request(path, { method: 'POST', body: JSON.stringify(data) })
export function query(values = {}) {
  return new URLSearchParams(Object.entries(values).filter(([, v]) => v !== '' && v != null)).toString()
}
export const api = {
  me: () => request('/api/admin/me'),
  login: (username, password) => post('/api/admin/login', { username, password }),
  logout: () => post('/api/admin/logout'),
  getSettings: () => request('/api/admin/settings'),
  saveSettings: settings => request('/api/admin/settings', { method: 'PUT', body: JSON.stringify(settings) }),
  testLLM: () => post('/api/admin/test-llm'),
  changePassword: password => post('/api/admin/password', { password }),
  refreshConcepts: () => post('/api/admin/concepts/refresh'),
  userMe: () => request('/api/auth/me'),
  userLogin: (username, password) => post('/api/auth/login', { username, password }),
  register: data => post('/api/auth/register', data),
  userLogout: () => post('/api/auth/logout'),
  userPassword: data => post('/api/auth/password', data),
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
  draft: (id, answers) => request(`/api/practice/sessions/${id}/answers`, { method: 'PUT', body: JSON.stringify({ answers }) }),
  submit: (id, answers) => post(`/api/practice/sessions/${id}/submit`, { answers }),
  records: f => request(`/api/practice/records?${query(f)}`),
  stats: () => request('/api/practice/stats'),
  rebuildStats: () => post('/api/practice/stats/rebuild'),
  wrongbook: f => request(`/api/wrongbook?${query(f)}`),
  wrongAction: (id, action) => post(`/api/wrongbook/${id}`, { action }),
  favorites: f => request(`/api/favorites?${query(f)}`),
  favoriteAction: (id, action) => post(`/api/favorites/${id}`, { action }),
}
