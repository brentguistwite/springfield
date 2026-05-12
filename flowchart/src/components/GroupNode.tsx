import type { NodeProps } from '@xyflow/react'

/** Custom group node that renders a header label inside the colored group box. */
export function GroupNode({ data }: NodeProps) {
  const label = data?.label as string | undefined
  return (
    <div
      className="group-node"
      style={{ width: '100%', height: '100%', position: 'relative' }}
    >
      {label ? (
        <div className="group-node__header">
          {label}
        </div>
      ) : null}
    </div>
  )
}
