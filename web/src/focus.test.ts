import { describe, expect, it } from 'vitest'
import { splitFocus, wipCount, wipLabel } from './focus'
import type { Worktree } from './types'
import type { Lane } from './stage'

const wt = (path: string): Worktree => ({ path, name: path } as Worktree)

describe('splitFocus', () => {
  it('moves starred worktrees to the focus strip, keeping order', () => {
    const list = [wt('a'), wt('b'), wt('c')]
    const { focus, rest } = splitFocus(list, { c: { starred: true }, a: { starred: true }, b: { text: 'x' } })
    expect(focus.map((w) => w.path)).toEqual(['a', 'c'])
    expect(rest.map((w) => w.path)).toEqual(['b'])
  })

  it('never takes the main worktree', () => {
    const main = { ...wt('m'), isMain: true } as Worktree
    expect(splitFocus([main], { m: { starred: true } }).focus).toEqual([])
  })
})

describe('wip', () => {
  const lanes = (counts: Record<string, number>): Lane[] =>
    Object.entries(counts).map(([stage, n]) => ({ stage: stage as Lane['stage'], items: Array.from({ length: n }, (_, i) => wt(`${stage}${i}`)) }))

  it('counts what is being worked on or waiting for you', () => {
    expect(wipCount(lanes({ 'needs-you': 2, 'in-progress': 3, review: 4, done: 5, dormant: 6 }))).toBe(5)
  })

  it('only warns above the limit', () => {
    expect(wipLabel(3, 3)).toBe('')
    expect(wipLabel(5, 3)).toBe('5 in progress, limit 3')
  })
})
