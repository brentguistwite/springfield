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
})
