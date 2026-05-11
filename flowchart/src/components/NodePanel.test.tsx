import { describe, it, expect, vi } from 'vitest'
import { act, render, screen, fireEvent } from '@testing-library/react'
import { NodePanel } from './NodePanel'
import type { LifecycleNode } from '../data/lifecycle'

const withSnippet: LifecycleNode = {
  id: 'plan-running',
  label: 'Running',
  description: 'Agent worktree is executing the plan.',
  machine: 'plan',
  cliSnippet: 'springfield status',
}

const withoutSnippet: LifecycleNode = {
  id: 'plan-completed',
  label: 'Completed',
  description: 'Agent finished cleanly; ready for merge integration.',
  machine: 'plan',
}

describe('NodePanel', () => {
  it('renders nothing when node is null', () => {
    const { container } = render(<NodePanel node={null} onClose={() => {}} />)
    expect(container.firstChild).toBeNull()
  })

  it('shows label, description, and snippet in <pre><code> when present', () => {
    render(<NodePanel node={withSnippet} onClose={() => {}} />)
    expect(screen.getByRole('heading', { name: /running/i })).toBeInTheDocument()
    expect(screen.getByText(withSnippet.description)).toBeInTheDocument()
    const code = screen.getByText('springfield status')
    expect(code.tagName).toBe('CODE')
    expect(code.closest('pre')).not.toBeNull()
  })

  it('omits snippet block when node has no cliSnippet', () => {
    render(<NodePanel node={withoutSnippet} onClose={() => {}} />)
    expect(screen.getByText(withoutSnippet.description)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /copy/i })).toBeNull()
    expect(document.querySelector('pre')).toBeNull()
  })

  it('closes on Escape', () => {
    const onClose = vi.fn()
    render(<NodePanel node={withSnippet} onClose={onClose} />)
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('closes on outside click', () => {
    const onClose = vi.fn()
    render(
      <div>
        <button data-testid="outside">outside</button>
        <NodePanel node={withSnippet} onClose={onClose} />
      </div>,
    )
    fireEvent.mouseDown(screen.getByTestId('outside'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('does not close when clicking inside the panel', () => {
    const onClose = vi.fn()
    render(<NodePanel node={withSnippet} onClose={onClose} />)
    fireEvent.mouseDown(screen.getByRole('heading', { name: /running/i }))
    expect(onClose).not.toHaveBeenCalled()
  })

  it('copies snippet to clipboard when copy button clicked', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })
    render(<NodePanel node={withSnippet} onClose={() => {}} />)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /copy/i }))
    })
    expect(writeText).toHaveBeenCalledWith('springfield status')
  })
})
