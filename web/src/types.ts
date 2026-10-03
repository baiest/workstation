// Mirrors the JSON produced by internal/workspace and internal/claude.

export type ClaudeStatus = 'working' | 'idle' | 'stopped' | 'unknown'

export interface Session {
  id: string
  desktopId?: string
  source: 'cli' | 'desktop'
  title?: string
  cwd: string
  originCwd?: string
  branch?: string
  status: ClaudeStatus
  rawStatus?: string
  lastActivity: string
  lastMessage?: string
  resumable: boolean
  slug?: string
  hasPlan: boolean
}

export interface Pr {
  number: number
  title: string
  url: string
  source: string
  dest: string
  state: 'open' | 'merged' | 'declined'
  draft: boolean
  approvals: number
  changesRequested: number
  review?: 'approved' | 'changes_requested' | 'review_required'
  checks?: 'success' | 'failure' | 'pending'
  updatedAt: string
}

export interface BranchNode {
  branch: string
  parent?: string
  via?: 'pr' | 'git'
  isDefault: boolean
  merged: boolean
  ahead: number
  behind: number
  tip: string
  date: string
  worktree?: { name: string; path: string }
  pr?: Pr
}

export interface BranchesResponse {
  graph: { default: string; nodes: BranchNode[]; hidden: number }
  warnings: string[]
  /** true when the graph came from git alone because no PR data was cached yet */
  prsPending?: boolean
}

export interface CleanupCandidate {
  branch: string
  sha: string
  prNumber: number
  prTitle: string
  prUrl?: string
  mergedAt: string
}

export interface CleanupSkipped {
  branch: string
  prNumber: number
  reason: string
}

export interface CleanupPreview {
  days: number
  candidates: CleanupCandidate[]
  skipped: CleanupSkipped[]
  warnings: string[]
}

export interface DeleteResult {
  branch: string
  sha: string
  deleted: boolean
  error?: string
}

export interface WtCandidate {
  path: string
  name: string
  branch: string
  sha: string
  prNumber: number
  prTitle: string
  prUrl?: string
  mergedAt: string
}

export interface WtSkipped {
  path: string
  name: string
  branch: string
  prNumber: number
  reason: string
}

export interface WtPreview {
  days: number
  candidates: WtCandidate[]
  skipped: WtSkipped[]
  warnings: string[]
}

export interface WtResult {
  path: string
  name?: string
  branch?: string
  sha: string
  removed: boolean
  error?: string
}

export interface PlanData {
  slug: string
  path: string
  markdown: string
  modifiedAt: string
}

export interface Commit {
  hash: string
  subject: string
  date: string
}

export interface GitInfo {
  dirty: boolean
  staged: number
  modified: number
  untracked: number
  conflicts: number
  hasUpstream: boolean
  ahead: number
  behind: number
  lastCommit?: Commit
}

export interface Worktree {
  name: string
  path: string
  branch?: string
  head: string
  detached: boolean
  isMain: boolean
  git: GitInfo
  gitError?: string
  sessions: Session[]
}

export interface Repo {
  name: string
  path: string
  worktrees: Worktree[]
}

export interface Unlinked {
  session: Session
  reason: string
}

export interface WorkspaceData {
  repos: Repo[]
  unlinked: Unlinked[]
  warnings: string[]
  generatedAt: string
  capabilities: { editor: string }
}
