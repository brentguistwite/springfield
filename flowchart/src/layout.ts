import dagre from 'dagre'
import { MarkerType, type Edge, type Node, type SmoothStepPathOptions } from '@xyflow/react'
import type {
  LifecycleEdge,
  LifecycleMachine,
  LifecycleNode,
} from './data/lifecycle'

const NODE_W = 180
const NODE_H = 52
const GROUP_PADDING = 24
const GROUP_PADDING_TOP = 44

// LR order: queue dispatches → plan executes → merge integrates
const MACHINES: LifecycleMachine[] = ['queue', 'plan', 'merge']

const MACHINE_LABEL: Record<LifecycleMachine, string> = {
  plan: 'Plan machine',
  queue: 'Queue machine',
  merge: 'Merge machine',
}

const MACHINE_BG: Record<LifecycleMachine, string> = {
  plan: 'rgba(59,130,246,0.07)',
  queue: 'rgba(168,85,247,0.07)',
  merge: 'rgba(16,185,129,0.07)',
}

const MACHINE_BORDER: Record<LifecycleMachine, string> = {
  plan: '#3b82f6',
  queue: '#a855f7',
  merge: '#10b981',
}

// Recovery edges routed as lighter back-arcs
const RECOVERY_EDGE_IDS = new Set([
  'plan-interrupted__running',
  'plan-failed__pending',
  'queue-halted__running',
  'queue-stopped__running',
  'merge-refused__pending',
  'merge-failed__pending',
])

// Parallel outgoing edges need different smoothstep offset to spread paths
// (positive offset = bend right/down, negative = bend left/up)
const EDGE_PATH_OFFSET: Record<string, number> = {
  // plan-running has 3 outgoing edges
  'plan-running__completed': 0,
  'plan-running__failed': 50,
  'plan-running__interrupted': -50,
  // queue-running has 3 outgoing edges
  'queue-running__completed': 0,
  'queue-running__halted': 50,
  'queue-running__stopped': -50,
  // merge-pending has 3 outgoing edges
  'merge-pending__succeeded': 0,
  'merge-pending__refused': 50,
  'merge-pending__failed': -50,
}

export interface LayoutResult {
  nodes: Node[]
  edges: Edge[]
}

export function layoutNodes(
  dataNodes: LifecycleNode[],
  dataEdges: LifecycleEdge[],
  direction: 'TB' | 'LR' = 'LR',
): LayoutResult {
  const childPositions = new Map<string, { x: number; y: number }>()
  const groupDims = new Map<LifecycleMachine, { width: number; height: number }>()

  // Inner layout is always TB — state diagram reads top-to-bottom within each group
  for (const m of MACHINES) {
    const machineNodes = dataNodes.filter((n) => n.machine === m)
    if (machineNodes.length === 0) continue
    const idSet = new Set(machineNodes.map((n) => n.id))
    const machineEdges = dataEdges.filter(
      (e) => idSet.has(e.source) && idSet.has(e.target),
    )

    const g = new dagre.graphlib.Graph()
    g.setGraph({ rankdir: 'TB', nodesep: 28, ranksep: 56, marginx: 0, marginy: 0 })
    g.setDefaultEdgeLabel(() => ({}))
    for (const n of machineNodes) g.setNode(n.id, { width: NODE_W, height: NODE_H })
    for (const e of machineEdges) g.setEdge(e.source, e.target)
    dagre.layout(g)

    let minX = Infinity
    let minY = Infinity
    let maxX = -Infinity
    let maxY = -Infinity
    for (const n of machineNodes) {
      const dn = g.node(n.id)
      minX = Math.min(minX, dn.x - NODE_W / 2)
      minY = Math.min(minY, dn.y - NODE_H / 2)
      maxX = Math.max(maxX, dn.x + NODE_W / 2)
      maxY = Math.max(maxY, dn.y + NODE_H / 2)
    }
    const offsetX = GROUP_PADDING - minX
    const offsetY = GROUP_PADDING_TOP - minY
    for (const n of machineNodes) {
      const dn = g.node(n.id)
      childPositions.set(n.id, {
        x: dn.x - NODE_W / 2 + offsetX,
        y: dn.y - NODE_H / 2 + offsetY,
      })
    }
    groupDims.set(m, {
      width: maxX - minX + GROUP_PADDING * 2,
      height: maxY - minY + GROUP_PADDING_TOP + GROUP_PADDING,
    })
  }

  const machineByNode = new Map(dataNodes.map((n) => [n.id, n.machine] as const))
  const gg = new dagre.graphlib.Graph()
  const groupRanksep = direction === 'LR' ? 140 : 100
  const groupNodesep = direction === 'LR' ? 60 : 80
  gg.setGraph({ rankdir: direction, nodesep: groupNodesep, ranksep: groupRanksep, marginx: 24, marginy: 24 })
  gg.setDefaultEdgeLabel(() => ({}))
  for (const m of MACHINES) {
    const dim = groupDims.get(m)
    if (!dim) continue
    gg.setNode(groupId(m), { width: dim.width, height: dim.height })
  }
  const seenGroupEdges = new Set<string>()
  for (const e of dataEdges) {
    const sm = machineByNode.get(e.source)
    const tm = machineByNode.get(e.target)
    if (!sm || !tm || sm === tm) continue
    const key = `${sm}->${tm}`
    if (seenGroupEdges.has(key)) continue
    seenGroupEdges.add(key)
    gg.setEdge(groupId(sm), groupId(tm))
  }
  dagre.layout(gg)

  const groupPositions = new Map<LifecycleMachine, { x: number; y: number }>()
  for (const m of MACHINES) {
    const dim = groupDims.get(m)
    if (!dim) continue
    const dn = gg.node(groupId(m))
    groupPositions.set(m, {
      x: dn.x - dim.width / 2,
      y: dn.y - dim.height / 2,
    })
  }

  const groupNodes: Node[] = MACHINES.filter((m) => groupDims.has(m)).map((m) => {
    const dim = groupDims.get(m)!
    return {
      id: groupId(m),
      type: 'group',
      data: { label: MACHINE_LABEL[m] },
      position: groupPositions.get(m)!,
      style: {
        width: dim.width,
        height: dim.height,
        background: MACHINE_BG[m],
        border: `1px solid ${MACHINE_BORDER[m]}`,
        borderRadius: 10,
      },
      draggable: false,
      selectable: false,
    }
  })

  const dataReactNodes: Node[] = dataNodes.map((n) => ({
    id: n.id,
    parentId: groupId(n.machine),
    extent: 'parent',
    data: {
      label: n.label,
      description: n.description,
      cliSnippet: n.cliSnippet,
      machine: n.machine,
    },
    position: childPositions.get(n.id) ?? { x: GROUP_PADDING, y: GROUP_PADDING_TOP },
    style: {
      width: NODE_W,
      height: NODE_H,
    },
  }))

  const reactEdges: Edge[] = dataEdges.map((e) => {
    const isRecovery = RECOVERY_EDGE_IDS.has(e.id)
    const pathOffset = EDGE_PATH_OFFSET[e.id]
    const color = edgeColor(e.kind)

    const edge: Edge = {
      id: e.id,
      source: e.source,
      target: e.target,
      type: 'smoothstep',
      label: e.label,
      labelShowBg: true,
      labelStyle: { fontSize: 11, fill: color, fontWeight: 500 },
      labelBgStyle: { fill: '#0b1220', fillOpacity: 0.9 },
      labelBgPadding: [4, 6] as [number, number],
      labelBgBorderRadius: 3,
      markerEnd: { type: MarkerType.ArrowClosed, color },
      style: {
        stroke: color,
        strokeDasharray: edgeDash(e.kind),
        strokeWidth: isRecovery ? 1.5 : 2,
        opacity: isRecovery ? 0.75 : 1,
      },
      animated: e.kind === 'fallback',
    }

    // Stagger parallel edges by adding a smoothstep path offset
    if (pathOffset !== undefined) {
      const opts: SmoothStepPathOptions = { borderRadius: 12 }
      if (pathOffset !== 0) opts.offset = pathOffset
      ;(edge as Edge & { pathOptions?: SmoothStepPathOptions }).pathOptions = opts
    }

    return edge
  })

  return { nodes: [...groupNodes, ...dataReactNodes], edges: reactEdges }
}

function groupId(m: LifecycleMachine): string {
  return `${m}-group`
}

function edgeColor(k: LifecycleEdge['kind']): string {
  switch (k) {
    case 'failure':
      return '#ef4444'
    case 'recovery':
      return '#10b981'
    case 'fallback':
      return '#a855f7'
    default:
      return '#64748b'
  }
}

function edgeDash(k: LifecycleEdge['kind']): string | undefined {
  switch (k) {
    case 'recovery':
      return '4 2'
    case 'fallback':
      return '2 4'
    default:
      return undefined
  }
}
