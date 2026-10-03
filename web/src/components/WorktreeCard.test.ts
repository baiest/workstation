import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import WorktreeCard from './WorktreeCard.vue'
import type { Session, Worktree } from '../types'

const now = new Date('2026-10-02T12:00:00Z')

const session = (over: Partial<Session> = {}): Session => ({
  id: 's1', source: 'cli', cwd: 'C:\\r\\wt', status: 'working',
  lastActivity: '2026-10-02T11:58:00Z', resumable: true, hasPlan: false, ...over,
})

const worktree = (over: Partial<Worktree> = {}): Worktree => ({
  name: 'REG-5413', path: 'C:\\r\\REG-5413', branch: 'feature/REG-5413-cycle-warning',
  head: 'abc1234', detached: false, isMain: false,
  git: {
    dirty: true, staged: 0, modified: 4, untracked: 1, conflicts: 0, hasUpstream: true, ahead: 2, behind: 0,
    lastCommit: { hash: 'abc123', subject: 'Add cycle warning', date: '2026-10-02T09:00:00Z' },
  },
  sessions: [session()],
  ...over,
})

const render = (wt: Worktree, editor = 'cursor') =>
  mount(WorktreeCard, { props: { worktree: wt, editor, now } })

describe('WorktreeCard', () => {
  it('shows name, branch, path, git, last commit and claude state', () => {
    const text = render(worktree()).text()
    expect(text).toContain('REG-5413')
    expect(text).toContain('feature/REG-5413-cycle-warning')
    expect(text).toContain('C:\\r\\REG-5413')
    expect(text).toContain('4 modified · 1 untracked')
    expect(text).toContain('↑2')
    expect(text).toContain('abc123')
    expect(text).toContain('Add cycle warning')
    expect(text).toContain('Working')
    expect(text).toContain('2 min ago')
  })

  it('marks the card with the newest session status', () => {
    expect(render(worktree()).classes()).toContain('status-working')
    expect(render(worktree({ sessions: [] })).classes()).toContain('status-none')
  })

  it('says so when there is no claude session', () => {
    expect(render(worktree({ sessions: [] })).text()).toContain('No Claude session')
  })

  it('shows detached head and the main badge', () => {
    const text = render(worktree({ branch: undefined, detached: true, isMain: true })).text()
    expect(text).toContain('detached @ abc1234')
    expect(text).toContain('main')
  })

  it('shows the git error instead of counts', () => {
    const text = render(worktree({ gitError: 'path missing' })).text()
    expect(text).toContain('path missing')
    expect(text).not.toContain('clean')
  })

  it('counts extra sessions', () => {
    const wt = worktree({ sessions: [session(), session({ id: 's2' }), session({ id: 's3' })] })
    expect(render(wt).text()).toContain('+2 more')
  })

  it('emits actions with the right payloads', async () => {
    const w = render(worktree())
    await w.get('[data-action=terminal]').trigger('click')
    await w.get('[data-action=editor]').trigger('click')
    await w.get('[data-action=resume]').trigger('click')
    expect(w.emitted('terminal')![0]).toEqual(['C:\\r\\REG-5413'])
    expect(w.emitted('editor')![0]).toEqual(['C:\\r\\REG-5413'])
    expect(w.emitted('resume')![0]).toEqual(['s1'])
  })

  it('hides editor when none is available and resume when nothing is resumable', () => {
    const w = render(worktree({ sessions: [session({ resumable: false })] }), '')
    expect(w.find('[data-action=editor]').exists()).toBe(false)
    expect(w.find('[data-action=resume]').exists()).toBe(false)
    expect(w.find('[data-action=terminal]').exists()).toBe(true)
  })

  it('offers the plan of the newest session that has one', async () => {
    const wt = worktree({ sessions: [session({ id: 'new' }), session({ id: 'planned', hasPlan: true }), session({ id: 'old', hasPlan: true })] })
    const w = render(wt)
    await w.get('[data-action=plan]').trigger('click')
    expect(w.emitted('plan')![0]).toEqual(['planned'])
  })

  it('hides the plan button when no session has a plan', () => {
    expect(render(worktree()).find('[data-action=plan]').exists()).toBe(false)
  })

  it('shows the PR status when known', () => {
    const pr = {
      number: 120, title: 't', url: 'https://x/pull/120', source: 'a', dest: 'main', state: 'open' as const,
      draft: false, approvals: 1, changesRequested: 0, review: 'approved' as const, updatedAt: '2026-10-01T00:00:00Z',
    }
    const w = mount(WorktreeCard, { props: { worktree: worktree(), editor: 'cursor', now, pr } })
    expect(w.text()).toContain('#120 Open · 1 approval')
    expect(w.get('a[href="https://x/pull/120"]').attributes('target')).toBe('_blank')
  })

  describe('title: ticket + PR title', () => {
    const prFor = (over: object = {}) => ({
      number: 48080, title: 'Update: use the ACE field in the builder', url: 'https://x/48080', source: 'REG-5393-ace-sync-customer-rules',
      dest: 'main', state: 'open' as const, draft: false, approvals: 0, changesRequested: 0, updatedAt: '2026-10-01T00:00:00Z', ...over,
    })
    const card = (wt: Worktree, pr?: ReturnType<typeof prFor>) =>
      mount(WorktreeCard, { props: { worktree: wt, editor: 'cursor', now, pr } })
    const REG = worktree({ name: 'REG-5393-ace-sync-worker', branch: 'REG-5393-ace-sync-customer-rules' })

    it('uses the PR title as the title, the branch under it, and the ticket as a badge', () => {
      const w = card(REG, prFor())
      expect(w.get('h3').text()).toBe('Update: use the ACE field in the builder')
      expect(w.get('.branch').text()).toContain('REG-5393-ace-sync-customer-rules')
      expect(w.get('.ticket').text()).toBe('REG-5393')
    })

    it('shows the folder separately, since it often differs from the branch', () => {
      const w = card(REG, prFor())
      expect(w.get('.folder').text()).toBe('REG-5393-ace-sync-worker')
    })

    it('without a PR the branch is the title and is not repeated underneath', () => {
      const w = card(REG)
      expect(w.get('h3').text()).toBe('REG-5393-ace-sync-customer-rules')
      expect(w.find('.branch').exists()).toBe(false)
      expect(w.get('.ticket').text()).toBe('REG-5393')
    })

    it('has no ticket badge for a branch without a key', () => {
      expect(card(worktree({ branch: 'feat/max-bid-increment' })).find('.ticket').exists()).toBe(false)
    })

    it('keeps the repository name as the title of the main worktree', () => {
      const w = card(worktree({ name: 'scrap-services', branch: 'main', isMain: true }), prFor({ source: 'main' }))
      expect(w.get('h3').text()).toBe('scrap-services')
      expect(w.get('.branch').text()).toContain('main')
    })
  })

  it('says when the PR is merged and nothing is pending, and not otherwise', () => {
    const merged = {
      number: 1, title: 't', url: 'https://x/1', source: 'a', dest: 'main', state: 'merged' as const,
      draft: false, approvals: 0, changesRequested: 0, updatedAt: '2026-10-01T00:00:00Z',
    }
    const clean = worktree({ git: { ...worktree().git, dirty: false, modified: 0, untracked: 0 }, sessions: [] })
    expect(mount(WorktreeCard, { props: { worktree: clean, editor: '', now, pr: merged } }).find('.merged-hint').exists()).toBe(true)
    expect(mount(WorktreeCard, { props: { worktree: worktree(), editor: '', now, pr: merged } }).find('.merged-hint').exists()).toBe(false) // dirty
    expect(mount(WorktreeCard, { props: { worktree: clean, editor: '', now } }).find('.merged-hint').exists()).toBe(false) // no PR
  })

  it('marks worktrees with a recent commit, not old ones', () => {
    const recent = render(worktree({ git: { ...worktree().git, lastCommit: { hash: 'h', subject: 's', date: '2026-10-01T12:00:00Z' } } }))
    expect(recent.classes()).toContain('fresh')
    const old = render(worktree({ git: { ...worktree().git, lastCommit: { hash: 'h', subject: 's', date: '2026-08-01T12:00:00Z' } } }))
    expect(old.classes()).not.toContain('fresh')
  })

  it('does not make a link out of a hostile PR url', () => {
    const pr = {
      number: 9, title: 't', url: 'javascript:alert(1)', source: 'a', dest: 'main', state: 'open' as const,
      draft: false, approvals: 0, changesRequested: 0, updatedAt: '2026-10-01T00:00:00Z',
    }
    const w = mount(WorktreeCard, { props: { worktree: worktree(), editor: 'cursor', now, pr } })
    expect(w.text()).toContain('#9 Open')
    expect(w.html()).not.toContain('javascript:')
    expect(w.find('a.pr').attributes('href')).toBeUndefined()
  })

  it('resumes the newest resumable session, skipping non-resumable ones', async () => {
    const wt = worktree({ sessions: [session({ id: 'new', resumable: false }), session({ id: 'old' })] })
    const w = render(wt)
    await w.get('[data-action=resume]').trigger('click')
    expect(w.emitted('resume')![0]).toEqual(['old'])
  })
})
