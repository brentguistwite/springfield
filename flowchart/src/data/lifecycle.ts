export type LifecycleMachine = 'plan' | 'queue' | 'merge'
export type LifecycleEdgeKind = 'normal' | 'fallback' | 'failure' | 'recovery'

export interface LifecycleNode {
  id: string
  label: string
  description: string
  machine: LifecycleMachine
  cliSnippet?: string
}

export interface LifecycleEdge {
  id: string
  source: string
  target: string
  kind: LifecycleEdgeKind
  label?: string
}

export const nodes: LifecycleNode[] = [
  // plan machine — mirrors PlanStatus consts in internal/features/conductor/types.go
  {
    id: 'plan-pending',
    label: 'Pending',
    description: 'Plan registered, awaiting dispatch by the conductor.',
    machine: 'plan',
    cliSnippet: 'springfield plan --prd - < envelope.json',
  },
  {
    id: 'plan-running',
    label: 'Running',
    description: 'Agent worktree is executing the plan.',
    machine: 'plan',
    cliSnippet: 'springfield status',
  },
  {
    id: 'plan-interrupted',
    label: 'Interrupted',
    description: 'Run halted mid-flight; resumable from the recorded cursor.',
    machine: 'plan',
    cliSnippet: 'springfield start  # resumes from cursor',
  },
  {
    id: 'plan-completed',
    label: 'Completed',
    description: 'Agent finished cleanly; ready for merge integration.',
    machine: 'plan',
  },
  {
    id: 'plan-failed',
    label: 'Failed',
    description: 'Agent run terminated with an error.',
    machine: 'plan',
    cliSnippet: 'springfield recover --diagnose --plan <plan-id>',
  },

  // queue machine — mirrors QueueStatus consts
  {
    id: 'queue-idle',
    label: 'Idle',
    description: 'No queue run in flight (empty-string QueueStatus const).',
    machine: 'queue',
    cliSnippet: 'springfield start',
  },
  {
    id: 'queue-running',
    label: 'Running',
    description: 'Sequential queue run dispatching plans one by one.',
    machine: 'queue',
    cliSnippet: 'springfield status',
  },
  {
    id: 'queue-completed',
    label: 'Completed',
    description: 'All registered plan units integrated successfully.',
    machine: 'queue',
  },
  {
    id: 'queue-halted',
    label: 'Halted',
    description: 'Queue stopped because a plan failed or refused to merge.',
    machine: 'queue',
    cliSnippet: 'springfield recover --diagnose --plan <plan-id>',
  },
  {
    id: 'queue-stopped',
    label: 'Stopped',
    description: 'Operator stopped the queue mid-run.',
    machine: 'queue',
    cliSnippet: 'springfield start  # resumes the queue',
  },

  // merge machine — mirrors MergeStatus consts
  {
    id: 'merge-pending',
    label: 'Pending',
    description: 'Plan finished; merge integration awaits.',
    machine: 'merge',
  },
  {
    id: 'merge-refused',
    label: 'Refused',
    description: 'Target branch head moved past the recorded base — strict policy refused the merge.',
    machine: 'merge',
    cliSnippet: 'springfield recover --diagnose --plan <plan-id>',
  },
  {
    id: 'merge-succeeded',
    label: 'Succeeded',
    description: 'Plan branch merged into target; cleanup runs next.',
    machine: 'merge',
  },
  {
    id: 'merge-failed',
    label: 'Failed',
    description: 'Merge attempt errored (conflict or git refusal).',
    machine: 'merge',
    cliSnippet: 'springfield recover --diagnose --plan <plan-id>',
  },
]

export const edges: LifecycleEdge[] = [
  // plan transitions
  { id: 'plan-pending__running', source: 'plan-pending', target: 'plan-running', kind: 'normal', label: 'dispatch' },
  { id: 'plan-running__completed', source: 'plan-running', target: 'plan-completed', kind: 'normal', label: 'success' },
  { id: 'plan-running__failed', source: 'plan-running', target: 'plan-failed', kind: 'failure', label: 'error' },
  { id: 'plan-running__interrupted', source: 'plan-running', target: 'plan-interrupted', kind: 'failure', label: 'signal/exit' },
  { id: 'plan-interrupted__running', source: 'plan-interrupted', target: 'plan-running', kind: 'recovery', label: 'resume' },
  { id: 'plan-failed__pending', source: 'plan-failed', target: 'plan-pending', kind: 'recovery', label: 'recover' },

  // queue transitions
  { id: 'queue-idle__running', source: 'queue-idle', target: 'queue-running', kind: 'normal', label: 'start' },
  { id: 'queue-running__completed', source: 'queue-running', target: 'queue-completed', kind: 'normal', label: 'all done' },
  { id: 'queue-running__halted', source: 'queue-running', target: 'queue-halted', kind: 'failure', label: 'plan failed' },
  { id: 'queue-running__stopped', source: 'queue-running', target: 'queue-stopped', kind: 'failure', label: 'operator stop' },
  { id: 'queue-halted__running', source: 'queue-halted', target: 'queue-running', kind: 'recovery', label: 'resume' },
  { id: 'queue-stopped__running', source: 'queue-stopped', target: 'queue-running', kind: 'recovery', label: 'resume' },

  // merge transitions
  { id: 'merge-pending__succeeded', source: 'merge-pending', target: 'merge-succeeded', kind: 'normal', label: 'merged' },
  { id: 'merge-pending__refused', source: 'merge-pending', target: 'merge-refused', kind: 'failure', label: 'base moved' },
  { id: 'merge-pending__failed', source: 'merge-pending', target: 'merge-failed', kind: 'failure', label: 'conflict' },
  { id: 'merge-refused__pending', source: 'merge-refused', target: 'merge-pending', kind: 'recovery', label: 'rebase' },
  { id: 'merge-failed__pending', source: 'merge-failed', target: 'merge-pending', kind: 'recovery', label: 'retry' },

  // cross-machine: queue dispatches plans; plan completion feeds merge; merge success advances queue
  { id: 'queue-running__plan-pending', source: 'queue-running', target: 'plan-pending', kind: 'fallback', label: 'dispatch plan' },
  { id: 'plan-completed__merge-pending', source: 'plan-completed', target: 'merge-pending', kind: 'normal', label: 'integrate' },
  { id: 'merge-succeeded__queue-running', source: 'merge-succeeded', target: 'queue-running', kind: 'normal', label: 'next plan' },
]

export const EXPECTED_PLAN_NODE_COUNT = 5
export const EXPECTED_QUEUE_NODE_COUNT = 5
export const EXPECTED_MERGE_NODE_COUNT = 4
