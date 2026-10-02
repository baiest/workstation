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

  it('shows a loading placeholder instead of "no PR" while pull requests load', () => {
    const loading = render({ prsLoading: true })
    expect(loading.get('[data-branch="old"]').text()).toContain('PR…')
    expect(loading.get('[data-branch="old"]').text()).not.toContain('no PR')
    expect(loading.get('[data-branch="old"]').classes()).toContain('pr-loading')
    // nodes whose PR is already known keep showing it
    expect(loading.get('[data-branch="feat-a"]').text()).toContain('#5 Open')

    const loaded = render({ prsLoading: false })
    expect(loaded.get('[data-branch="old"]').text()).toContain('no PR')
    expect(loaded.get('[data-branch="old"]').classes()).not.toContain('pr-loading')
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

  describe('zoom', () => {
    const scaleOf = (w: ReturnType<typeof render>) => w.get('.graph').attributes('style')
    it('starts at 100% and zooms with the buttons', async () => {
      const w = render()
      expect(w.get('[data-zoom=label]').text()).toBe('100%')
      expect(scaleOf(w)).toContain('scale(1)')

      await w.get('[data-zoom=in]').trigger('click')
      expect(w.get('[data-zoom=label]').text()).toBe('110%')
      expect(scaleOf(w)).toContain('scale(1.1)')

      await w.get('[data-zoom=out]').trigger('click')
      await w.get('[data-zoom=out]').trigger('click')
      expect(w.get('[data-zoom=label]').text()).toBe('90%')
    })

    it('stops at the limits and resets', async () => {
      const w = render()
      for (let i = 0; i < 40; i++) await w.get('[data-zoom=out]').trigger('click')
      expect(w.get('[data-zoom=label]').text()).toBe('30%')
      for (let i = 0; i < 40; i++) await w.get('[data-zoom=in]').trigger('click')
      expect(w.get('[data-zoom=label]').text()).toBe('200%')
      await w.get('[data-zoom=reset]').trigger('click')
      expect(w.get('[data-zoom=label]').text()).toBe('100%')
    })

    it('zooms with ctrl + wheel and ignores a plain wheel', async () => {
      const w = render()
      const el = w.get('.graph-scroll').element
      const wheel = async (deltaY: number, ctrlKey: boolean) => {
        const ev = new WheelEvent('wheel', { deltaY, ctrlKey, bubbles: true, cancelable: true })
        el.dispatchEvent(ev)
        await w.vm.$nextTick()
        return ev
      }

      const zoomed = await wheel(-100, true)
      expect(w.get('[data-zoom=label]').text()).toBe('110%')
      expect(zoomed.defaultPrevented).toBe(true) // the browser must not zoom the whole page too
      await wheel(100, true)
      expect(w.get('[data-zoom=label]').text()).toBe('100%')
      const plain = await wheel(-100, false)
      expect(w.get('[data-zoom=label]').text()).toBe('100%')
      expect(plain.defaultPrevented).toBe(false) // a plain wheel keeps scrolling the graph
    })

    it('keeps the scrollable area in step with the zoom', async () => {
      const w = render()
      const before = w.get('.graph-zoom').attributes('style')
      await w.get('[data-zoom=in]').trigger('click')
      expect(w.get('.graph-zoom').attributes('style')).not.toBe(before)
    })
  })

  describe('search', () => {
    it('highlights matches and dims the rest', async () => {
      const w = render()
      await w.get('[data-search]').setValue('feat-b')
      expect(w.get('[data-branch="feat-b"]').classes()).toContain('match')
      expect(w.get('[data-branch="feat-a"]').classes()).toContain('dim')
      expect(w.get('[data-branch="main"]').classes()).toContain('dim')
      expect(w.get('[data-search-count]').text()).toBe('1 match')
    })

    it('finds a branch by PR number', async () => {
      const w = render()
      await w.get('[data-search]').setValue('#6')
      expect(w.findAll('.gnode.match').map((n) => n.attributes('data-branch'))).toEqual(['feat-b'])
    })

    it('says when nothing matches and clears with the button', async () => {
      const w = render()
      await w.get('[data-search]').setValue('zzz')
      expect(w.get('[data-search-count]').text()).toBe('no matches')
      expect(w.findAll('.gnode.dim')).toHaveLength(4)

      await w.get('[data-search-clear]').trigger('click')
      expect(w.findAll('.gnode.dim')).toHaveLength(0)
      expect((w.get('[data-search]').element as HTMLInputElement).value).toBe('')
    })

    it('Enter selects the next match, cycling', async () => {
      const w = render()
      await w.get('[data-search]').setValue('feat')
      expect(w.get('[data-search-count]').text()).toBe('2 matches')

      await w.get('[data-search]').trigger('keydown', { key: 'Enter' })
      expect(w.get('.detail h3').text()).toBe('feat-a')
      await w.get('[data-search]').trigger('keydown', { key: 'Enter' })
      expect(w.get('.detail h3').text()).toBe('feat-b')
      await w.get('[data-search]').trigger('keydown', { key: 'Enter' })
      expect(w.get('.detail h3').text()).toBe('feat-a')
    })

    it('Escape clears the search', async () => {
      const w = render()
      await w.get('[data-search]').setValue('feat')
      await w.get('[data-search]').trigger('keydown', { key: 'Escape' })
      expect(w.findAll('.gnode.dim')).toHaveLength(0)
    })
  })

  it('sizes nodes from the rem prop so the UI size control applies to the graph', () => {
    const small = render({ rem: 16 })
    const big = render({ rem: 24 })
    const widthOf = (w: ReturnType<typeof render>) => parseFloat(w.get('[data-branch="main"]').attributes('style')!.match(/width: ([\d.]+)px/)![1])
    expect(widthOf(big)).toBeGreaterThan(widthOf(small))
  })

  it('closes the detail panel when the same node is clicked again', async () => {
    const w = render()
    await w.get('[data-branch="old"]').trigger('click')
    await w.get('[data-branch="old"]').trigger('click')
    expect(w.find('.detail').exists()).toBe(false)
  })
})
