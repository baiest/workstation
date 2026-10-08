// Layered left-to-right tree layout for the branch graph. Pure: no DOM.

export interface LayoutNode {
  branch: string
  parent?: string
  isDefault?: boolean
}

export interface LayoutOptions {
  nodeW: number
  nodeH: number
  gapX: number
  gapY: number
}

export interface Position {
  x: number
  y: number
  col: number
  row: number
}

export interface Layout {
  positions: Map<string, Position>
  edges: { from: string; to: string }[]
  width: number
  height: number
}

/**
 * Columns are depth, rows are leaves in name order; a parent sits centred on
 * its children. Nodes whose parent is unknown, or that are unreachable from the
 * root (cycles), hang off the root so nothing disappears.
 */
export function layoutTree(nodes: LayoutNode[], o: LayoutOptions): Layout {
  const positions = new Map<string, Position>()
  if (nodes.length === 0) return { positions, edges: [], width: 0, height: 0 }

  const root = nodes.find((n) => n.isDefault) ?? nodes[0]
  const names = new Set(nodes.map((n) => n.branch))
  const parentOf = new Map<string, string>()
  for (const n of nodes) {
    if (n.branch === root.branch) continue
    parentOf.set(n.branch, n.parent && n.parent !== n.branch && names.has(n.parent) ? n.parent : root.branch)
  }
  attachUnreachable(parentOf, root.branch)

  const children = new Map<string, string[]>()
  for (const [child, parent] of parentOf) children.set(parent, [...(children.get(parent) ?? []), child])
  for (const list of children.values()) list.sort()

  let nextRow = 0
  const place = (branch: string, col: number): number => {
    const kids = children.get(branch) ?? []
    let row: number
    if (kids.length === 0) {
      row = nextRow++
    } else {
      const rows = kids.map((k) => place(k, col + 1))
      row = (rows[0] + rows[rows.length - 1]) / 2
    }
    positions.set(branch, { col, row, x: col * (o.nodeW + o.gapX), y: row * (o.nodeH + o.gapY) })
    return row
  }
  place(root.branch, 0)

  const edges = [...parentOf].map(([to, from]) => ({ from, to }))
  edges.sort((a, b) => order(nodes, a.to) - order(nodes, b.to))

  const cols = Math.max(...[...positions.values()].map((p) => p.col)) + 1
  const maxRow = Math.max(...[...positions.values()].map((p) => p.row))
  return {
    positions,
    edges,
    width: cols * o.nodeW + (cols - 1) * o.gapX,
    height: (maxRow + 1) * o.nodeH + maxRow * o.gapY,
  }
}

const order = (nodes: LayoutNode[], branch: string) => nodes.findIndex((n) => n.branch === branch)

/** Re-parents to the root any node whose parent chain never reaches the root. */
function attachUnreachable(parentOf: Map<string, string>, root: string) {
  for (const start of [...parentOf.keys()]) {
    let cur = start
    const seen = new Set<string>()
    while (cur !== root && !seen.has(cur)) {
      seen.add(cur)
      cur = parentOf.get(cur) ?? root
    }
    if (cur !== root) parentOf.set(start, root)
  }
}

/** Cubic curve from the right edge of the parent to the left edge of the child. */
export function edgePath(from: { x: number; y: number }, to: { x: number; y: number }, nodeW: number, nodeH: number): string {
  const x1 = from.x + nodeW
  const y1 = from.y + nodeH / 2
  const x2 = to.x
  const y2 = to.y + nodeH / 2
  const mid = x1 + (x2 - x1) / 2
  return `M ${x1} ${y1} C ${mid} ${y1}, ${mid} ${y2}, ${x2} ${y2}`
}

export interface DagNode {
  id: string
  deps: string[] // ids this node waits for: edges go from each of them to it
}

export interface DagLayout {
  positions: Map<string, Position>
  edges: { from: string; to: string }[]
  width: number
  height: number
}

/**
 * Layered left-to-right layout of a dependency graph: a node sits one column
 * right of the furthest of the nodes it waits for, so every edge points forward.
 * Dependencies on nodes that are not in the list are ignored, and a cycle (the
 * server refuses them, but data can be old) cannot hang it: its nodes are placed
 * by the longest chain that does not revisit a node.
 */
export function layoutDag(nodes: DagNode[], o: LayoutOptions): DagLayout {
  const positions = new Map<string, Position>()
  if (nodes.length === 0) return { positions, edges: [], width: 0, height: 0 }

  const ids = new Set(nodes.map((n) => n.id))
  const deps = new Map(nodes.map((n) => [n.id, [...new Set(n.deps)].filter((d) => ids.has(d) && d !== n.id)]))

  const col = new Map<string, number>()
  const depth = (id: string, path: Set<string>): number => {
    const known = col.get(id)
    if (known !== undefined) return known
    if (path.has(id)) return 0
    path.add(id)
    const c = Math.max(-1, ...(deps.get(id) ?? []).map((d) => depth(d, path))) + 1
    path.delete(id)
    col.set(id, c)
    return c
  }
  for (const n of nodes) depth(n.id, new Set())

  // rows: in each column, keep the order of the input, but pull a node towards the average row of what it waits for
  const columns = new Map<number, string[]>()
  for (const n of nodes) columns.set(col.get(n.id)!, [...(columns.get(col.get(n.id)!) ?? []), n.id])
  const row = new Map<string, number>()
  const cols = [...columns.keys()].sort((a, b) => a - b)
  for (const c of cols) {
    const want = (id: string) => {
      const rows = (deps.get(id) ?? []).map((d) => row.get(d)).filter((r): r is number => r !== undefined)
      return rows.length ? rows.reduce((a, b) => a + b, 0) / rows.length : Infinity
    }
    const order = columns.get(c)!.map((id, i) => ({ id, i, w: want(id) }))
    order.sort((a, b) => (a.w === b.w ? a.i - b.i : a.w - b.w))
    order.forEach((e, r) => row.set(e.id, r))
  }

  for (const n of nodes) {
    const c = col.get(n.id)!
    const r = row.get(n.id)!
    positions.set(n.id, { col: c, row: r, x: c * (o.nodeW + o.gapX), y: r * (o.nodeH + o.gapY) })
  }
  const edges = nodes.flatMap((n) => (deps.get(n.id) ?? []).map((from) => ({ from, to: n.id })))
  const maxCol = Math.max(...cols)
  const maxRow = Math.max(...[...row.values()])
  return {
    positions,
    edges,
    width: (maxCol + 1) * o.nodeW + maxCol * o.gapX,
    height: (maxRow + 1) * o.nodeH + maxRow * o.gapY,
  }
}
