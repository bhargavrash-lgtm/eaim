import {
  LayoutDashboard,
  Monitor,
  Bot,
  ShieldCheck,
  Wrench,
  Network,
  ClipboardCheck,
  DollarSign,
  Brain,
  ScrollText,
  Bell,
  Settings,
  type LucideIcon,
} from 'lucide-react'

export interface NavItem {
  label: string
  path: string
  icon: LucideIcon
  group: 'main' | 'gateway' | 'ops' | 'admin'
  badgeKey?: 'pendingApprovals'
}

export const NAV_ITEMS: NavItem[] = [
  { label: 'Dashboard', path: '/dashboard', icon: LayoutDashboard, group: 'main' },
  { label: 'Discover', path: '/discover', icon: Monitor, group: 'main' },
  { label: 'Agents', path: '/gateway/agents', icon: Bot, group: 'gateway' },
  { label: 'Policies', path: '/gateway/policies', icon: ShieldCheck, group: 'gateway' },
  { label: 'Tools', path: '/gateway/tools', icon: Wrench, group: 'gateway' },
  { label: 'Nodes', path: '/gateway/nodes', icon: Network, group: 'gateway' },
  { label: 'Approvals', path: '/approvals', icon: ClipboardCheck, group: 'ops', badgeKey: 'pendingApprovals' },
  { label: 'FinOps', path: '/finops', icon: DollarSign, group: 'ops' },
  { label: 'Memory', path: '/memory', icon: Brain, group: 'ops' },
  { label: 'Audit', path: '/audit', icon: ScrollText, group: 'ops' },
  { label: 'Alerts', path: '/alerts', icon: Bell, group: 'ops' },
  { label: 'Settings', path: '/settings', icon: Settings, group: 'admin' },
]

export const NAV_GROUPS: { key: NavItem['group']; label: string }[] = [
  { key: 'main', label: 'Overview' },
  { key: 'gateway', label: 'Gateway' },
  { key: 'ops', label: 'Operations' },
  { key: 'admin', label: 'Admin' },
]
