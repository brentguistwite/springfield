import { useCallback, useMemo, useState, useEffect } from 'react'
import {
  Background,
  BackgroundVariant,
  Controls,
  ReactFlow,
  type Edge,
  type Node,
  type NodeTypes,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { edges as dataEdges, nodes as dataNodes } from './data/lifecycle'
import type { LifecycleNode } from './data/lifecycle'
import { layoutNodes } from './layout'
import { NodePanel } from './components/NodePanel'
import { GroupNode } from './components/GroupNode'
import { Legend } from './components/Legend'
import './App.css'

export const PLAN_PRIMARY_PATH = ['plan-pending', 'plan-running', 'plan-completed'] as const

// activeEdgeIdForStep encodes the edge-id convention from src/data/lifecycle.ts:
// `<source-node-id>__<target-status>` where target-status is the node id with
// the `plan-` machine prefix stripped. Exported so App.test.tsx can pin the
// convention against the real edges in lifecycle.ts — a future rename of the
// convention will fail the test, not the UI.
export function activeEdgeIdForStep(step: number): string | null {
  const prevIdx = step - 2
  const currIdx = step - 1
  // Steps 0 and 1 have no incoming edge: step 0 = nothing highlighted,
  // step 1 = first node in path (no predecessor).
  if (prevIdx < 0 || currIdx < 0) return null
  return `${PLAN_PRIMARY_PATH[prevIdx]}__${PLAN_PRIMARY_PATH[currIdx].replace(/^plan-/, '')}`
}

const nodeTypes: NodeTypes = {
  group: GroupNode,
}

/** Returns 'LR' on wide screens, 'TB' on narrow. */
function useLayoutDirection(): 'LR' | 'TB' {
  const [dir, setDir] = useState<'LR' | 'TB'>(() =>
    typeof window !== 'undefined' && window.matchMedia('(min-width: 900px)').matches
      ? 'LR'
      : 'TB',
  )

  useEffect(() => {
    const mq = window.matchMedia('(min-width: 900px)')
    const handler = (e: MediaQueryListEvent) => setDir(e.matches ? 'LR' : 'TB')
    mq.addEventListener('change', handler)
    return () => mq.removeEventListener('change', handler)
  }, [])

  return dir
}

export default function App() {
  const direction = useLayoutDirection()

  const { nodes: laidOutNodes, edges: laidOutEdges } = useMemo(
    () => layoutNodes(dataNodes, dataEdges, direction),
    [direction],
  )
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [step, setStep] = useState(0)

  const dataById = useMemo(() => {
    const map = new Map<string, LifecycleNode>()
    for (const n of dataNodes) map.set(n.id, n)
    return map
  }, [])

  const selected = selectedId ? dataById.get(selectedId) ?? null : null

  const totalSteps = PLAN_PRIMARY_PATH.length
  const activeNodeId = step > 0 ? PLAN_PRIMARY_PATH[step - 1] : null
  const activeEdgeId = activeEdgeIdForStep(step)

  const activeLabel = activeNodeId ? dataById.get(activeNodeId)?.label ?? '' : ''
  const caption = activeNodeId
    ? `Step ${step} of ${totalSteps}: ${activeLabel}`
    : `Step 0 of ${totalSteps}`

  const nodes = useMemo<Node[]>(
    () =>
      laidOutNodes.map((n) =>
        n.id === activeNodeId
          ? { ...n, className: `${n.className ?? ''} lifecycle-node--active`.trim() }
          : n,
      ),
    [laidOutNodes, activeNodeId],
  )

  const edges = useMemo<Edge[]>(
    () =>
      laidOutEdges.map((e) =>
        e.id === activeEdgeId
          ? { ...e, className: `${e.className ?? ''} lifecycle-edge--active`.trim(), animated: true }
          : e,
      ),
    [laidOutEdges, activeEdgeId],
  )

  const onNodeClick = useCallback((_: unknown, node: Node) => {
    setSelectedId(node.id)
  }, [])

  const handleClose = useCallback(() => setSelectedId(null), [])
  const handleNext = useCallback(() => {
    setStep((s) => (s < totalSteps ? s + 1 : s))
  }, [totalSteps])
  const handleReset = useCallback(() => setStep(0), [])

  return (
    <div className="app">
      <header className="app-header">
        <h1>Springfield Lifecycle</h1>
        <div
          className="step-through"
          role="group"
          aria-label="Plan walkthrough"
          data-active-node={activeNodeId ?? ''}
          data-active-edge={activeEdgeId ?? ''}
        >
          <button
            type="button"
            className="step-through__button"
            onClick={handleNext}
            disabled={step >= totalSteps}
          >
            Next
          </button>
          <button
            type="button"
            className="step-through__button step-through__button--secondary"
            onClick={handleReset}
            disabled={step === 0}
          >
            Reset
          </button>
          <span className="step-through__caption" data-testid="step-caption" aria-live="polite">
            {caption}
          </span>
        </div>
      </header>
      <main className="app-main">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          fitView
          minZoom={0.2}
          proOptions={{ hideAttribution: true }}
          onNodeClick={onNodeClick}
        >
          <Controls />
          <Background variant={BackgroundVariant.Dots} gap={32} size={1} color="rgba(148,163,184,0.18)" />
          <div className="canvas-title">
            Springfield queue → plan → merge lifecycle
          </div>
          <Legend />
        </ReactFlow>
        <NodePanel node={selected} onClose={handleClose} />
      </main>
    </div>
  )
}
