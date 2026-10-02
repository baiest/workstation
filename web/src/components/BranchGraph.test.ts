import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import BranchGraph from './BranchGraph.vue'
import type { BranchNode, Pr, Session, Worktree } from '../types'

const now = new Date('2026-10-02T12:00:00Z')

const pr = (over: Partial<Pr> = {}): Pr => ({
  number: 5, title: 'Add cycle warning', url: 'https://x/pull/5', source: 'feat-a', dest: 'main', state: 'open',
  draft: false, approvals: 1, changesRequested: 0, review: 'approved', checks: 'success', updatedAt: '2026-10-01T00:00:00Z', ...over,
})

const node = (over: Partial<BranchNode> & { branch: string }): BranchNode => ({
  isDefault: false, merged: false, ahead: 0, behind: 0, tip: 'abc', date: '2026-10-01T00:00:00Z', ...over,
})

const session = (over: Partial<Session> = {}): Session => ({
  id: 's1', source: 'cli', cwd: '/w/a', status: 'working', lastActivity: '2026-10-02T11:58:00Z', resumable: true, hasPlan: true, ...over,
})

const worktree = (over: Partial<Worktree> = {}): Worktree => ({
  name: 'wt-a', path: '/w/a', branch: 'feat-a', head: 'abc1234', detached: false, isMain: false,
  git: { dirty: false, staged: 0, modified: 0, untracked: 0, conflicts: 0, hasUpstream: false, ahead: 0, behind: 0 },
  sessions: [session()], ...over,
})

const nodes: BranchNode[] = [
  node({ branch: 'main', isDefault: true }),
  node({ branch: 'feat-a', parent: 'main', via: 'git', ahead: 3, behind: 1, worktree: { name: 'wt-a', path: '/w/a' }, pr: pr() }),
  node({ branch: 'feat-b', parent: 'feat-a', via: 'pr', ahead: 1, pr: pr({ number: 6, source: 'feat-b', dest: 'feat-a', state: 'merged' }), merged: true }),
  node({ branch: 'old', parent: 'main', via: 'git', date: '2026-06-01T00:00:00Z' }),
]

const render = (over: Record<string, unknown> = {}) =>
  mount(BranchGraph, {
    props: { graph: { default: 'main', nodes, hidden: 0 }, worktrees: [worktree()], editor: 'cursor', now, ...over },
  })

describe('BranchGraph', () => {
  it('draws one node per branch and one edge per dependency', () => {
    const w = render()
    expect(w.findAll('.gnode')).toHaveLength(4)
    expect(w.findAll('.edge')).toHaveLength(3)
  })

  it('draws edges inferred from git dashed and PR-based edges solid', () => {
    const w = render()
    expect(w.findAll('.edge.inferred')).toHaveLength(2) // feat-a and old
    expect(w.findAll('.edge:not(.inferred)')).toHaveLength(1) // feat-b via PR
  })

  it('shows branch name, PR status and ahead/behind', () => {
    const text = render().get('[data-branch="feat-a"]').text()
    expect(text).toContain('feat-a')
    expect(text).toContain('#5 Open · 1 approval · checks ✓')
    expect(text).toContain('↑3')
    expect(text).toContain('↓1')
  })

  it('says when a branch has no PR', () => {
    expect(render().get('[data-branch="old"]').text()).toContain('no PR')
  })

  it('marks merged and stale branches', () => {
    const w = render()
    expect(w.get('[data-branch="feat-b"]').classes()).toContain('merged')
    expect(w.get('[data-branch="old"]').classes()).toContain('stale')
    expect(w.get('[data-branch="feat-a"]').classes()).not.toContain('stale')
  })

  it('shows the Claude status of the worktree on the node', () => {
    expect(render().get('[data-branch="feat-a"]').classes()).toContain('status-working')
    expect(render().get('[data-branch="old"]').classes()).toContain('status-none')
  })

  it('notes branches hidden by the cap', () => {
    expect(render({ graph: { default: 'main', nodes, hidden: 3 } }).text()).toContain('3 more branches hidden')
    expect(render().text()).not.toContain('hidden')
  })

  it('opens a detail panel on click with PR link, base and worktree actions', async () => {
    const w = render()
    expect(w.find('.detail').exists()).toBe(false)

    await w.get('[data-branch="feat-a"]').trigger('click')
    const detail = w.get('.detail')
    expect(detail.text()).toContain('Add cycle warning')
    expect(detail.text()).toContain('inferred from git history')
    expect(detail.text()).toContain('/w/a')
    expect(detail.get('a[href="https://x/pull/5"]').attributes('target')).toBe('_blank')

    await detail.get('[data-action=terminal]').trigger('click')
    await detail.get('[data-action=editor]').trigger('click')
    await detail.get('[data-action=resume]').trigger('click')
    await detail.get('[data-action=plan]').trigger('click')
    expect(w.emitted('terminal')![0]).toEqual(['/w/a'])
    expect(w.emitted('editor')![0]).toEqual(['/w/a'])
    expect(w.emitted('resume')![0]).toEqual(['s1'])
    expect(w.emitted('plan')![0]).toEqual(['s1'])
  })

  it('explains the base when it comes from the PR', async () => {
    const w = render()
    await w.get('[data-branch="feat-b"]').trigger('click')
    expect(w.get('.detail').text()).toContain('PR base')
  })

  it('offers no worktree actions for a branch without a worktree', async () => {
    const w = render()
    await w.get('[data-branch="old"]').trigger('click')
    expect(w.find('.detail [data-action=terminal]').exists()).toBe(false)
  })

  it('closes the detail panel when the same node is clicked again', async () => {
    const w = render()
    await w.get('[data-branch="old"]').trigger('click')
    await w.get('[data-branch="old"]').trigger('click')
    expect(w.find('.detail').exists()).toBe(false)
  })
})
