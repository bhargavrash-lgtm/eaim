// PolicyBadges.tsx -- shared policy-table badges, extracted from
// PoliciesPage.tsx (B-214) so the new workspace-scoped policy view (B-210)
// can reuse the exact same ScopeBadge/ConditionSummary/ActionBadge/
// StatusBadge implementation rather than reinventing it, per that brief's
// own explicit instruction. Neither Action/Status/Scope genuinely fits
// StatusPill's shape (rounded-full + capitalize vs. plain rounded, no
// capitalize) -- PoliciesPage.tsx's own original comment already reasoned
// through and rejected that reuse; these stay local-style badges, colors
// sourced from the shared status.*/brand.* design tokens, just promoted
// out of one page file since a second page now needs them too.
import type { Policy } from '@/hooks/usePolicies'

const ACTION_STYLES: Record<string, string> = {
  allow:    'bg-status-success text-status-success-text',
  deny:     'bg-status-danger text-status-danger-text',
  escalate: 'bg-status-warning text-status-warning-text',
}

const STATUS_STYLES: Record<string, string> = {
  active:   'bg-status-success text-status-success-text',
  draft:    'bg-gray-100 text-gray-600',
  disabled: 'bg-status-danger text-status-danger-text',
}

export function ActionBadge({ action }: { action: string }) {
  return (
    <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-semibold capitalize ${ACTION_STYLES[action] ?? 'bg-gray-100 text-gray-600'}`}>
      {action}
    </span>
  )
}

export function StatusBadge({ status }: { status: string }) {
  return (
    <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium capitalize ${STATUS_STYLES[status] ?? 'bg-gray-100 text-gray-600'}`}>
      {status}
    </span>
  )
}

// ScopeBadge -- DESIGN_SYSTEM.md §7.3: a workspace-scoped policy must be
// visibly distinguished from the org-wide floor. "Global floor" reuses
// STATUS_STYLES.draft's exact gray -- the same neutral treatment already
// shipped in this file, not a new color. The named-workspace case uses
// brand-50/brand-700, the existing token pair for DESIGN_SYSTEM.md §2's
// Accent (#3B5BDB) -- confirmed against the live Layer4 canvas mockup's
// own "HR-specific" badge (bg #EEF1FA/text #3B5BDB), same token family.
export function ScopeBadge({ workspaceName }: { workspaceName?: string | null }) {
  if (!workspaceName) {
    return (
      <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-600">
        Global floor
      </span>
    )
  }
  return (
    <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-brand-50 text-brand-700">
      {workspaceName}
    </span>
  )
}

export function ConditionSummary({ conditions }: { conditions: Policy['conditions'] }) {
  const parts: string[] = []
  if (conditions.agent_name_pattern) parts.push('agent: ' + conditions.agent_name_pattern)
  if (conditions.tool_names?.length) parts.push('tools: ' + conditions.tool_names.join(', '))
  if (conditions.action_types?.length) parts.push('actions: ' + conditions.action_types.join(', '))
  if (conditions.environments?.length) parts.push('env: ' + conditions.environments.join(', '))
  if (conditions.record_count_gt != null) parts.push('records > ' + conditions.record_count_gt)
  if (conditions.semantic_rule) parts.push('semantic rule set')
  if (conditions.scope_drift) parts.push('scope drift')
  if (parts.length === 0) return <span className="text-gray-400 italic text-xs">any request</span>
  return <span className="text-xs text-gray-600 truncate max-w-xs" title={parts.join(' / ')}>{parts.join(' / ')}</span>
}
