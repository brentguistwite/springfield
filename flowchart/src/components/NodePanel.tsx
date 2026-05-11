import { useEffect, useRef, useState } from 'react'
import type { LifecycleNode } from '../data/lifecycle'

interface NodePanelProps {
  node: LifecycleNode | null
  onClose: () => void
}

export function NodePanel({ node, onClose }: NodePanelProps) {
  const panelRef = useRef<HTMLElement | null>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!node) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    const onMouseDown = (event: MouseEvent) => {
      const target = event.target as Node | null
      if (panelRef.current && target && !panelRef.current.contains(target)) {
        onClose()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    document.addEventListener('mousedown', onMouseDown)
    return () => {
      window.removeEventListener('keydown', onKeyDown)
      document.removeEventListener('mousedown', onMouseDown)
    }
  }, [node, onClose])

  useEffect(() => {
    setCopied(false)
  }, [node?.id])

  if (!node) return null

  const handleCopy = async () => {
    if (!node.cliSnippet) return
    try {
      await navigator.clipboard.writeText(node.cliSnippet)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }

  return (
    <aside
      ref={panelRef}
      className="node-panel"
      role="complementary"
      aria-label="Node detail"
    >
      <header className="node-panel__header">
        <h2 className="node-panel__title">{node.label}</h2>
        <button
          type="button"
          className="node-panel__close"
          onClick={onClose}
          aria-label="Close panel"
        >
          ×
        </button>
      </header>
      <p className="node-panel__description">{node.description}</p>
      {node.cliSnippet ? (
        <div className="node-panel__snippet">
          <div className="node-panel__snippet-bar">
            <span className="node-panel__snippet-label">CLI</span>
            <button
              type="button"
              className="node-panel__copy"
              onClick={handleCopy}
            >
              {copied ? 'Copied' : 'Copy'}
            </button>
          </div>
          <pre>
            <code>{node.cliSnippet}</code>
          </pre>
        </div>
      ) : null}
    </aside>
  )
}
