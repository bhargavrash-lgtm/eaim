// AssetWorkspaceBadge -- B-196 increment 1.
//
// Extends PolicyBadges.tsx's ScopeBadge (DESIGN_SYSTEM.md §7.3) with a
// THIRD real state, for CI types that have no workspace_id column at all
// (gateway_tools, gateway_nodes -- confirmed live, not assumed) rather than
// just two. This is a deliberate Part A decision, not a styling
// afterthought: reusing ScopeBadge's plain "Global floor" gray for these
// rows would say "this asset could be workspace-scoped and simply isn't" --
// false for a CI type with no real mechanism to be scoped at all. Per
// DESIGN_SYSTEM.md §7.4 (Honest Data -- never imply a capability that
// isn't real), that structural absence gets its own visually distinct,
// explicitly-labeled state: a dashed border (the doc's own convention for
// "not yet available") + muted gray, never conflated with the real
// org-wide-floor case.
export function AssetWorkspaceBadge({
  scoped,
  workspaceName,
}: {
  // scoped: false for a CI type with no workspace_id column at all
  // (gateway_tools/gateway_nodes today). true for endpoints/gateway_agents,
  // which genuinely have the column, whether or not this particular row
  // carries a value.
  scoped: boolean
  workspaceName?: string | null
}) {
  if (!scoped) {
    return (
      <span
        title="This asset type has no workspace-scoping column yet -- it cannot be assigned to a workspace, not just currently unassigned."
        className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium border border-dashed border-gray-300 text-gray-400"
      >
        Not workspace-scoped
      </span>
    )
  }
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
