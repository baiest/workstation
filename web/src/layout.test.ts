import { describe, expect, it } from 'vitest'
import { edgePath, layoutTree } from './layout'

const opts = { nodeW: 100, nodeH: 40, gapX: 20, gapY: 10 }
const n = (branch: string, parent?: string, isDefault = false) => ({ branch, parent, isDefault })

describe('layoutTree', () => {
  it('places a lone root at the origin', () => {
    const l = layoutTree([n('main', undefined, true)], opts)
    expect(l.positions.get('main')).toEqual({ x: 0, y: 0, col: 0, row: 0 })
    expect(l.width).toBe(100)
    expect(l.height).toBe(40)
    expect(l.edges).toEqual([])
  })

  it('puts children one column right, one row each, parent centred', () => {
    const l = layoutTree([n('main', undefined, true), n('b', 'main'), n('a', 'main')], opts)
    expect(l.positions.get('a')).toMatchObject({ col: 1, row: 0, x: 120, y: 0 })
    expect(l.positions.get('b')).toMatchObject({ col: 1, row: 1, x: 120, y: 50 })
    expect(l.positions.get('main')).toMatchObject({ col: 0, row: 0.5, y: 25 })
    expect(l.width).toBe(220)
    expect(l.height).toBe(90)
  })

  it('sorts siblings by name for a stable layout', () => {
    const l = layoutTree([n('main', undefined, true), n('z', 'main'), n('m', 'main'), n('a', 'main')], opts)
    expect(['a', 'm', 'z'].map((b) => l.positions.get(b)!.row)).toEqual([0, 1, 2])
  })

  it('stacks chains to the right', () => {
    const l = layoutTree([n('main', undefined, true), n('a', 'main'), n('b', 'a')], opts)
    expect(l.positions.get('b')).toMatchObject({ col: 2, row: 0 })
    expect(l.edges).toEqual([
      { from: 'main', to: 'a' },
      { from: 'a', to: 'b' },
    ])
  })

  it('attaches nodes with an unknown parent to the root', () => {
    const l = layoutTree([n('main', undefined, true), n('x', 'ghost')], opts)
    expect(l.positions.get('x')).toMatchObject({ col: 1 })
    expect(l.edges).toEqual([{ from: 'main', to: 'x' }])
  })

  it('does not lose nodes caught in a parent cycle', () => {
    const l = layoutTree([n('main', undefined, true), n('a', 'b'), n('b', 'a')], opts)
    expect(l.positions.has('a')).toBe(true)
    expect(l.positions.has('b')).toBe(true)
  })

  it('handles an empty list', () => {
    const l = layoutTree([], opts)
    expect(l.positions.size).toBe(0)
    expect(l.width).toBe(0)
  })
})

describe('edgePath', () => {
  it('draws a curve from the right edge of the parent to the left edge of the child', () => {
    const d = edgePath({ x: 0, y: 25 }, { x: 120, y: 75 }, 100, 40)
    expect(d).toBe('M 100 45 C 110 45, 110 95, 120 95')
  })
})
