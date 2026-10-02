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
