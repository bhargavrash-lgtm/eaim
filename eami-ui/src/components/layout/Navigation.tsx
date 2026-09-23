import {
  LayoutDashboard,
  Monitor,
  Bot,
  ShieldCheck,
  Wrench,
  Workflow,
  Network,
  ClipboardCheck,
  DollarSign,
  Brain,
  ScrollText,
  Bell,
  Settings,
  Copy,
  Building2,
  Boxes,
  type LucideIcon,
} from 'lucide-react'

export interface NavItem {
  label: string
  path: string
  icon: LucideIcon
  group: 'main' | 'gateway' | 'approvals' | 'ops' | 'admin' | 'workspace'
  badgeKey?: 'pendingApprovals'
  // conditional (B-216): true only for an item that must be filtered out
  // of NAV_ITEMS entirely unless some live, per-user condition holds --
  // Sidebar.tsx is the only place that checks this. Every other item is
  // unconditional (undefined), rendered exactly as before this field
  // existed.
  conditional?: 'hasWorkspaces'
}

export const NAV_ITEMS: NavItem[] = [
  { label: 'Dashboard', path: '/dashboard', icon: LayoutDashboard, group: 'main' },
  { label: 'Discover', path: '/discover', icon: Monitor, group: 'main' },
  // Assets (B-196 increment 1, CMDB epic): a real, new top-level page, not
  // one of CLAUDE.md's six named core sidebar pages -- flagged explicitly
  // per the one-spine rule rather than silently added. Judged a legitimate
  // exception: it introduces no new identity model, audit trail, or
  // policy mechanism of its own -- it's a read-only normalization layer
  // over three of the six core pages' own real underlying data (Discover's
  // endpoints, Agents, Tools), reusing B-200's proven agent-connections
  // endpoint directly rather than building a parallel relationship
  // mechanism. It's also a long-scoped, founder-approved epic
  // (BACKLOG.md's B-196) with its own design-canvas backing (Layer 2b),
  // not an ad hoc addition.
  { label: 'Assets', path: '/assets', icon: Boxes, group: 'main' },
  { label: 'Agents', path: '/gateway/agents', icon: Bot, group: 'gateway' },
  { label: 'Policies', path: '/gateway/policies', icon: ShieldCheck, group: 'gateway' },
  { label: 'Tools', path: '/gateway/tools', icon: Wrench, group: 'gateway' },
  { label: 'Workflows', path: '/gateway/workflows', icon: Workflow, group: 'gateway' },
  { label: 'Nodes', path: '/gateway/nodes', icon: Network, group: 'gateway' },
  { label: 'Approvals', path: '/approvals', icon: ClipboardCheck, group: 'approvals', badgeKey: 'pendingApprovals' },
  { label: 'FinOps', path: '/finops', icon: DollarSign, group: 'ops' },
  { label: 'Memory', path: '/memory', icon: Brain, group: 'ops' },
  { label: 'Paste Detection', path: '/paste-events', icon: Copy, group: 'ops' },
  { label: 'Audit', path: '/audit', icon: ScrollText, group: 'ops' },
  { label: 'Alerts', path: '/alerts', icon: Bell, group: 'ops' },
  { label: 'Settings', path: '/settings', icon: Settings, group: 'admin' },
  // My Workspaces (B-216): the real entry point into Workspace mode
  // (DESIGN_SYSTEM.md §0) for a user who has real workspace_memberships
  // rows -- filtered out of both Sidebar render loops entirely (not just
  // visually hidden) when Sidebar.tsx's own live GET /v1/workspaces/mine
  // check finds none, so an ordinary Admin-only user never sees it. This
  // is a navigation entry point into an already-separate, already-
  // reviewed mode (B-215, mandatory reviewer+security passes), not a new
  // governance silo of its own -- it introduces no separate identity
  // model, audit trail, or policy mechanism; workspace-scoped policies
  // are real `policies` rows evaluated by the same evaluator as every
  // other policy. Flagged and reasoned through explicitly here per
  // CLAUDE.md's one-spine rule, rather than silently added.
  { label: 'My Workspaces', path: '/workspace', icon: Building2, group: 'workspace', conditional: 'hasWorkspaces' },
]

export const NAV_GROUPS: { key: NavItem['group']; label: string }[] = [
  { key: 'main', label: 'Overview' },
  { key: 'gateway', label: 'Gateway' },
  { key: 'approvals', label: 'Approvals' },
  { key: 'ops', label: 'Operations' },
  { key: 'admin', label: 'Admin' },
  { key: 'workspace', label: 'Workspace' },
]
