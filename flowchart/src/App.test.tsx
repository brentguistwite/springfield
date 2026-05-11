import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import App from './App'

describe('App', () => {
  it('renders the Springfield Lifecycle heading', () => {
    render(<App />)
    expect(
      screen.getByRole('heading', { name: /springfield lifecycle/i }),
    ).toBeInTheDocument()
  })

  it('opens NodePanel with snippet when a node with cliSnippet is clicked', () => {
    render(<App />)
    const nodeEl = document.querySelector('.react-flow__node[data-id="plan-running"]') as HTMLElement
    expect(nodeEl).not.toBeNull()
    fireEvent.click(nodeEl)
    const panel = screen.getByRole('complementary', { name: /node detail/i })
    expect(within(panel).getByRole('heading', { name: /running/i })).toBeInTheDocument()
    expect(within(panel).getByText('springfield status')).toBeInTheDocument()
  })

  it('opens NodePanel without snippet block for nodes lacking cliSnippet', () => {
    render(<App />)
    const nodeEl = document.querySelector('.react-flow__node[data-id="plan-completed"]') as HTMLElement
    expect(nodeEl).not.toBeNull()
    fireEvent.click(nodeEl)
    const panel = screen.getByRole('complementary', { name: /node detail/i })
    expect(within(panel).getByRole('heading', { name: /completed/i })).toBeInTheDocument()
    expect(within(panel).queryByRole('button', { name: /copy/i })).toBeNull()
  })

  it('closes NodePanel on Escape', () => {
    render(<App />)
    const nodeEl = document.querySelector('.react-flow__node[data-id="plan-running"]') as HTMLElement
    fireEvent.click(nodeEl)
    expect(screen.getByRole('complementary', { name: /node detail/i })).toBeInTheDocument()
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(screen.queryByRole('complementary', { name: /node detail/i })).toBeNull()
  })

  describe('step-through indicator', () => {
    const getCaption = () => screen.getByTestId('step-caption').textContent ?? ''
    const isActive = (id: string) =>
      document.querySelector(`.react-flow__node[data-id="${id}"]`)?.classList.contains('lifecycle-node--active') ?? false
    const activeEdge = () =>
      document.querySelector('.step-through')?.getAttribute('data-active-edge') ?? ''

    it('starts at step 0 with no active highlight', () => {
      render(<App />)
      expect(getCaption()).toMatch(/Step 0 of 3/)
      expect(isActive('plan-pending')).toBe(false)
      expect(isActive('plan-running')).toBe(false)
      expect(isActive('plan-completed')).toBe(false)
    })

    it('advances to pending on first Next click', () => {
      render(<App />)
      fireEvent.click(screen.getByRole('button', { name: /next/i }))
      expect(getCaption()).toBe('Step 1 of 3: Pending')
      expect(isActive('plan-pending')).toBe(true)
    })

    it('reaches completed after three Next clicks', () => {
      render(<App />)
      const next = screen.getByRole('button', { name: /next/i })
      fireEvent.click(next)
      fireEvent.click(next)
      fireEvent.click(next)
      expect(getCaption()).toBe('Step 3 of 3: Completed')
      expect(isActive('plan-completed')).toBe(true)
      expect(activeEdge()).toBe('plan-running__completed')
    })

    it('disables Next at the final step', () => {
      render(<App />)
      const next = screen.getByRole('button', { name: /next/i }) as HTMLButtonElement
      fireEvent.click(next)
      fireEvent.click(next)
      fireEvent.click(next)
      expect(next.disabled).toBe(true)
      fireEvent.click(next)
      expect(getCaption()).toBe('Step 3 of 3: Completed')
    })

    it('Reset returns to step 0 with no highlight', () => {
      render(<App />)
      const next = screen.getByRole('button', { name: /next/i })
      fireEvent.click(next)
      fireEvent.click(next)
      fireEvent.click(screen.getByRole('button', { name: /reset/i }))
      expect(getCaption()).toMatch(/Step 0 of 3/)
      expect(isActive('plan-pending')).toBe(false)
      expect(isActive('plan-running')).toBe(false)
    })

    it('highlights the active edge entering the current node', () => {
      render(<App />)
      const next = screen.getByRole('button', { name: /next/i })
      fireEvent.click(next) // step 1: pending — no incoming edge
      expect(activeEdge()).toBe('')
      fireEvent.click(next) // step 2: running — edge pending->running active
      expect(activeEdge()).toBe('plan-pending__running')
    })
  })
})
