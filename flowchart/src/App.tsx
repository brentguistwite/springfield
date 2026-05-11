import { useCallback, useMemo, useState } from 'react'
import { Background, Controls, ReactFlow, type Node } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { edges as dataEdges, nodes as dataNodes } from './data/lifecycle'
import type { LifecycleNode } from './data/lifecycle'
import { layoutNodes } from './layout'
import { NodePanel } from './components/NodePanel'
import './App.css'

export default function App() {
  const { nodes, edges } = useMemo(() => layoutNodes(dataNodes, dataEdges), [])
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const dataById = useMemo(() => {
    const map = new Map<string, LifecycleNode>()
    for (const n of dataNodes) map.set(n.id, n)
    return map
  }, [])

  const selected = selectedId ? dataById.get(selectedId) ?? null : null

  const onNodeClick = useCallback((_: unknown, node: Node) => {
    setSelectedId(node.id)
  }, [])

  const handleClose = useCallback(() => setSelectedId(null), [])

  return (
    <div className="app">
      <header className="app-header">
        <h1>Springfield Lifecycle</h1>
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
