import { useMemo } from 'react'
import { Background, Controls, ReactFlow } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { edges as dataEdges, nodes as dataNodes } from './data/lifecycle'
import { layoutNodes } from './layout'
import './App.css'

export default function App() {
  const { nodes, edges } = useMemo(() => layoutNodes(dataNodes, dataEdges), [])

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
        >
          <Controls />
          <Background gap={20} />
        </ReactFlow>
      </main>
    </div>
  )
}
