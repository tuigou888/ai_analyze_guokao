import { reactive } from 'vue'
import { api, setRequestOwner } from './api'
export const auth = reactive({ user: null, admin: null, profile: null, identityChanged: false })
// Only the currently verified user owns this in-memory preference cache.
let profileGeneration = 0
let profileUserID = null
// Signals invalidate local assumptions only; only /auth/me establishes identity.
const channel = typeof BroadcastChannel === 'undefined' ? null : new BroadcastChannel('gk-account-identity')
function invalidateIdentity() { auth.identityChanged = true; profileGeneration++; auth.profile = null; profileUserID = null }
channel?.addEventListener('message', e => {
  if (!auth.user) return
  const signal = e.data
  if (signal?.kind === 'login' && signal.id !== auth.user.id || signal?.kind === 'logout' && signal.id === auth.user.id) invalidateIdentity()
})
window.addEventListener('account-changed', e => { if (auth.user && e.detail === auth.user.id) invalidateIdentity() })
export function clearUser({ announce = false, owner = auth.user?.id } = {}) {
  setRequestOwner(null)
  auth.identityChanged = false
  if (announce) channel?.postMessage({ kind: 'logout', id: owner })
  profileGeneration++
  auth.user = null
  auth.profile = null
  profileUserID = null
}
export function setUser(user, { announce = false } = {}) {
  setRequestOwner(user?.id ?? null)
  auth.identityChanged = false
  if (announce) channel?.postMessage({ kind: 'login', id: user?.id ?? null })
  if (profileUserID !== user?.id) { profileGeneration++; auth.profile = null; profileUserID = null }
  auth.user = user
  if (profileUserID === user?.id && auth.profile?.username === user.username) auth.user.nickname = auth.profile.nickname
}
export function applyProfile(profile, userID = auth.user?.id) {
  if (auth.identityChanged || !auth.user || userID !== auth.user.id || profile.username !== auth.user.username) return false
  // A valid response from this user may be older than a completed write.
  // Treat it as an ignored read, not an identity error, and retain the high-water mark.
  const confirmed = profileUserID === userID && auth.profile && auth.profile.revision > profile.revision ? auth.profile : profile
  profileUserID = auth.user?.id ?? null
  auth.profile = confirmed
  auth.user.nickname = confirmed.nickname
  return true
}
export async function loadProfile() {
  const id = auth.user?.id, generation = ++profileGeneration
  if (id == null) return null
  const profile = await api.profile()
  if (generation === profileGeneration && auth.user?.id === id && applyProfile(profile, id)) return auth.profile
  return null
}
