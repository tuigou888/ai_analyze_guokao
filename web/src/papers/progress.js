// Only route to a real saved attempt for this paper. A missing entry means no
// attempt; callers must handle loading/errors separately before enabling start.
export function paperAction(paperId, progress) {
  if (!progress || Number(progress.paper_id) !== Number(paperId) || !Number.isSafeInteger(progress.session_id) || progress.session_id <= 0) {
    return { kind: 'start', label: '开始作答' }
  }
  if (!['in_progress', 'completed'].includes(progress.status)) return { kind: 'start', label: '开始作答' }
  return {
    kind: progress.status === 'in_progress' ? 'resume' : 'review',
    label: progress.status === 'in_progress' ? '继续作答' : '查看复盘',
    sessionId: progress.session_id,
    answered: progress.answered,
    total: progress.total,
  }
}

export function ownsPaperProgress(owner, userId, identityChanged) {
  return owner != null && owner === userId && !identityChanged
}
