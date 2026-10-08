import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import DependencyGraph from './DependencyGraph.vue'
import { buildTickets } from '../tickets'
import type { StoredTicket, TicketState, Worktree } from '../types'

const wt = (name: string): Worktree => ({
  name, path: `/r/${name}`, branch: name, head: 'abc', detached: false, isMain: false, ticket: name.toUpperCase(),
  git: { dirty: false, staged: 0, modified: 0, untracked: 0, conflicts: 0, hasUpstream: true, ahead: 0, behind: 0 },
  sessions: [],
})

function graph(spec: [string, TicketState, string[]?][]) {
  const stored: Record<string, StoredTicket> = {}
  for (const [name, state, deps] of spec) {
    const ref = name.toUpperCase()
    stored[`/r|${ref}`] = { id: `/r|${ref}`, repo: '/r', ref, state, blockedBy: (deps ?? []).map((d) => `/r|${d.toUpperCase()}`), created: '', updated: '' }
  }
  const tickets = buildTickets(spec.map(([n]) => wt(n)), [], 'main', { repo: '/r', stored })
  return mount(DependencyGraph, { props: { tickets, repoNames: { '/r': 'Repo' } } })
}

describe('DependencyGraph', () => {
  it('says how to start when nothing waits for anything', () => {
    expect(graph([['a', 'active']]).text()).toContain('No dependencies yet')
  })

  it('draws only the tickets that take part, with one arrow per dependency', () => {
    const w = graph([['a', 'done'], ['b', 'blocked', ['a']], ['c', 'blocked', ['a', 'b']], ['alone', 'active']])
    expect(w.findAll('[data-dag-node]').map((n) => n.attributes('data-dag-node')).sort()).toEqual(['/r|A', '/r|B', '/r|C'])
    expect(w.findAll('path.dag-edge')).toHaveLength(3)
  })

  it('marks what has been waited for and is finished, and says what can be unblocked', () => {
    const w = graph([['a', 'done'], ['b', 'blocked', ['a']]])
    expect(w.findAll('path.dag-edge.settled')).toHaveLength(1)
    expect(w.get('[data-unblockable-list]').text()).toContain('B')
    expect(graph([['a', 'active'], ['b', 'blocked', ['a']]]).find('[data-unblockable-list]').exists()).toBe(false)
  })

  it('puts a ticket to the right of what it waits for', () => {
    const w = graph([['a', 'active'], ['b', 'blocked', ['a']]])
    const left = (id: string) => parseFloat((w.get(`[data-dag-node="${id}"]`).element as HTMLElement).style.left)
    expect(left('/r|B')).toBeGreaterThan(left('/r|A'))
  })
})
