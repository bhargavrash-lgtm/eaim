import type { ReactNode } from 'react'

interface SlideOverPanelProps {
  onClose: () => void
  // true only for a panel rendered ON TOP OF another already-open
  // SlideOverPanel (currently just WorkflowsPage.tsx's StepConfigPanel,
  // opened from inside AddWorkflowPanel/EditWorkflowPanel) -- bumps this
  // panel's own backdrop/shell above the parent panel's z-40/z-50 instead
  // of colliding with it.
  nested?: boolean
  children: ReactNode
}

// Single shared shell for every slide-out panel in the app: owns the
// backdrop (with click-outside-to-close) and the panel's width/border/
// z-index. A panel using this component structurally cannot omit the
// backdrop the way AuditEntryDetailPanel.tsx previously did -- that's
// the real bug this component closes, not just a style unification.
//
// Deliberately instant, no enter/exit transition: every one of the 9
// panels this replaces was already instant, so "instant" is the real
// existing baseline, not an omission. A correct exit animation for a
// conditionally-mounted panel (every caller unmounts it immediately on
// close) needs real mount/unmount-timing coordination -- a transition
// library, or custom exit-delay state -- which is new complexity on 9
// currently-working real interactive surfaces for a cosmetic gain, not
// a requested fix. Left as a deliberate, documented non-decision, not
// silently defaulted either way -- a clean follow-up if ever wanted.
//
// Width is fixed (spacing.drawer, 480px), not a per-caller prop: a
// shared component only prevents future drawer-width drift if it owns
// the one value outright, the same reasoning that closed AgentsPage's
// w-96/PoliciesPage/ToolsPage/WorkflowsPage's mixed 420/440/480 split.
export function SlideOverPanel({ onClose, nested = false, children }: SlideOverPanelProps) {
  return (
    <>
      <div
        className={`fixed inset-0 bg-black/20 ${nested ? 'z-[55]' : 'z-40'}`}
        onClick={onClose}
      />
      <div
        className={`fixed inset-y-0 right-0 w-drawer bg-white shadow-xl flex flex-col border-l border-gray-200 ${nested ? 'z-[60]' : 'z-50'}`}
      >
        {children}
      </div>
    </>
  )
}
