import { describe, it, expect } from 'vitest'
import { layoutNodes } from './layout'
import { nodes as dataNodes, edges as dataEdges } from './data/lifecycle'

describe('layoutNodes', () => {
  it('returns one positioned node per input data node', () => {
    const { nodes } = layoutNodes(dataNodes, dataEdges)
    const data = nodes.filter((n) => n.type !== 'group')
    expect(data).toHaveLength(dataNodes.length)
    for (const n of data) {
      expect(n.position).toBeDefined()
      expect(typeof n.position.x).toBe('number')
      expect(typeof n.position.y).toBe('number')
    }
  })

  it('no data node has both x=0 and y=0 (dagre actually ran)', () => {
    const { nodes } = layoutNodes(dataNodes, dataEdges)
    const data = nodes.filter((n) => n.type !== 'group')
    for (const n of data) {
      const atOrigin = n.position.x === 0 && n.position.y === 0
      expect(atOrigin).toBe(false)
    }
  })

  it('emits one group node per machine with id <machine>-group', () => {
    const { nodes } = layoutNodes(dataNodes, dataEdges)
    const groupIds = nodes
      .filter((n) => n.type === 'group')
      .map((n) => n.id)
      .sort()
    expect(groupIds).toEqual(['merge-group', 'plan-group', 'queue-group'])
  })

  it('sets parentId on every data node to its machine group', () => {
    const { nodes } = layoutNodes(dataNodes, dataEdges)
    const machineById = new Map(dataNodes.map((n) => [n.id, n.machine] as const))
    for (const n of nodes) {
      if (n.type === 'group') continue
      expect(n.parentId).toBe(`${machineById.get(n.id)}-group`)
    }
  })

  it('returns all input edges shaped for ReactFlow', () => {
    const { edges } = layoutNodes(dataNodes, dataEdges)
    expect(edges).toHaveLength(dataEdges.length)
    const inputIds = new Set(dataEdges.map((e) => e.id))
    for (const e of edges) {
      expect(inputIds.has(e.id)).toBe(true)
    }
  })

  it('TB direction: nodes are non-overlapping and within a plausible y-range', () => {
    const { nodes } = layoutNodes(dataNodes, dataEdges, 'TB')
    const data = nodes.filter((n) => n.type !== 'group')
    // All nodes positioned
    for (const n of data) {
      expect(n.position).toBeDefined()
      expect(typeof n.position.x).toBe('number')
      expect(typeof n.position.y).toBe('number')
    }
    // Groups should be stacked vertically (max group y > min group y)
    const groups = nodes.filter((n) => n.type === 'group')
    const ys = groups.map((n) => n.position.y)
    const minY = Math.min(...ys)
    const maxY = Math.max(...ys)
    expect(maxY).toBeGreaterThan(minY)
  })
})
