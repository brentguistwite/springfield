import { useCallback, useMemo, useState } from 'react'
import { Background, Controls, ReactFlow, type Edge, type Node } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { edges as dataEdges, nodes as dataNodes } from './data/lifecycle'
import type { LifecycleNode } from './data/lifecycle'
import { layoutNodes } from './layout'
import { NodePanel } from './components/NodePanel'
import './App.css'

export const PLAN_PRIMARY_PATH = ['plan-pending', 'plan-running', 'plan-completed'] as const

// activeEdgeIdForStep encodes the edge-id convention from src/data/lifecycle.ts:
// `<source-node-id>__<target-status>` where target-status is the node id with
// the `plan-` machine prefix stripped. Exported so App.test.tsx can pin the
// convention against the real edges in lifecycle.ts — a future rename of the
// convention will fail the test, not the UI.
export function activeEdgeIdForStep(step: number): string | null {
  if (step < 2) return null
  const prev = PLAN_PRIMARY_PATH[step - 2]
  const curr = PLAN_PRIMARY_PATH[step - 1]
  return `${prev}__${curr.replace(/^plan-/, '')}`
}

export default function App() {
  const { nodes: laidOutNodes, edges: laidOutEdges } = useMemo(
    () => layoutNodes(dataNodes, dataEdges),
    [],
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
          fitView
          minZoom={0.2}
          proOptions={{ hideAttribution: true }}
          onNodeClick={onNodeClick}
        >
          <Controls />
          <Background gap={20} />
        </ReactFlow>
        <NodePanel node={selected} onClose={handleClose} />
      </main>
    </div>
  )
}
