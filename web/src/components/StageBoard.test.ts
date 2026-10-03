import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import StageBoard from './StageBoard.vue'
import type { Session, Worktree } from '../types'

const now = new Date('2026-10-02T12:00:00Z')
const ago = (days: number) => new Date(now.getTime() - days * 86400_000).toISOString()
const wt = (name: string, days: number, sessions: Session[] = [], isMain = false): Worktree => ({
  name, path: `/r/${name}`, branch: `REG-${name.length}-${name}`, head: 'abc', detached: false, isMain,
  git: { dirty: false, staged: 0, modified: 0, untracked: 0, conflicts: 0, hasUpstream: true, ahead: 0, behind: 0,
    lastCommit: { hash: 'h', subject: 's', date: ago(days) } },
  sessions,
})
const waiting: Session = { id: 's', source: 'cli', cwd: '/w', status: 'idle', state: 'waiting', lastActivity: ago(0.1), resumable: true, hasPlan: false }

const board = (list: Worktree[]) =>
  mount(StageBoard, { props: { worktrees: list, editor: 'cursor', prFor: () => undefined, now } })

describe('StageBoard', () => {
  it('puts each worktree in its lane, with a count', () => {
    const w = board([wt('main', 1, [], true), wt('needs', 1, [waiting]), wt('busy', 1)])
    expect(w.get('[data-lane=needs-you]').text()).toContain('Needs you')
    expect(w.get('[data-lane=needs-you] .card').text()).toContain('needs')
    expect(w.get('[data-lane=in-progress] .count').text()).toBe('1')
    expect(w.find('[data-lane=dormant]').exists()).toBe(false)
  })

  it('shows the main worktree above the lanes', () => {
    const w = board([wt('main', 1, [], true), wt('busy', 1)])
    expect(w.get('[data-main] .card').text()).toContain('main')
  })

  it('collapses the dormant lane until asked', async () => {
    const w = board([wt('old', 40)])
    expect(w.find('[data-lane=dormant] .card').exists()).toBe(false)
    await w.get('[data-lane=dormant] [data-toggle-lane]').trigger('click')
    expect(w.find('[data-lane=dormant] .card').exists()).toBe(true)
  })

  it('passes card actions up', async () => {
    const w = board([wt('busy', 1)])
    await w.get('[data-action=terminal]').trigger('click')
    expect(w.emitted('terminal')![0]).toEqual(['/r/busy'])
  })
})
