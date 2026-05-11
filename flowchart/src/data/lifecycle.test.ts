import { describe, it, expect } from 'vitest'
import {
  nodes,
  edges,
  EXPECTED_PLAN_NODE_COUNT,
  EXPECTED_QUEUE_NODE_COUNT,
  EXPECTED_MERGE_NODE_COUNT,
} from './lifecycle'

const VALID_MACHINES = new Set(['plan', 'queue', 'merge'])
const VALID_KINDS = new Set(['normal', 'fallback', 'failure', 'recovery'])

describe('lifecycle data', () => {
  it('every edge source + target references an existing node id', () => {
    const ids = new Set(nodes.map((n) => n.id))
    for (const edge of edges) {
      expect(ids.has(edge.source), `edge ${edge.id} source ${edge.source}`).toBe(true)
      expect(ids.has(edge.target), `edge ${edge.id} target ${edge.target}`).toBe(true)
    }
  })

  it('every node has a valid machine value', () => {
    for (const node of nodes) {
      expect(VALID_MACHINES.has(node.machine), `node ${node.id} machine ${node.machine}`).toBe(true)
    }
  })

  it('every edge has a valid kind', () => {
    for (const edge of edges) {
      expect(VALID_KINDS.has(edge.kind), `edge ${edge.id} kind ${edge.kind}`).toBe(true)
    }
  })

  it('EXPECTED_*_NODE_COUNT constants match live counts', () => {
    const planCount = nodes.filter((n) => n.machine === 'plan').length
    const queueCount = nodes.filter((n) => n.machine === 'queue').length
    const mergeCount = nodes.filter((n) => n.machine === 'merge').length
    expect(planCount).toBe(EXPECTED_PLAN_NODE_COUNT)
    expect(queueCount).toBe(EXPECTED_QUEUE_NODE_COUNT)
    expect(mergeCount).toBe(EXPECTED_MERGE_NODE_COUNT)
  })

  it('node ids are unique', () => {
    const ids = nodes.map((n) => n.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('edge ids are unique', () => {
    const ids = edges.map((e) => e.id)
    expect(new Set(ids).size).toBe(ids.length)
  })
})
