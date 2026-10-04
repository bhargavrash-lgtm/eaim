import { createBrowserRouter, Navigate, useLocation, useParams } from 'react-router-dom'
import { AppShell } from '@/components/layout/AppShell'
import { WorkspaceShell } from '@/components/layout/WorkspaceShell'
import { WorkspaceOverviewPage } from '@/pages/workspace/WorkspaceOverviewPage'
import { WorkspacePoliciesPage } from '@/pages/workspace/WorkspacePoliciesPage'
import { LoginPage } from '@/pages/auth/LoginPage'
import { AcceptInvitePage } from '@/pages/auth/AcceptInvitePage'
import { ForgotPasswordPage } from '@/pages/auth/ForgotPasswordPage'
import { ResetPasswordPage } from '@/pages/auth/ResetPasswordPage'
import { ProfilePage } from '@/pages/settings/ProfilePage'
import { SetupWizardPage } from '@/pages/setup/SetupWizardPage'
import { DashboardPage } from '@/pages/dashboard/DashboardPage'
import { AssetsPage } from '@/pages/cmdb/AssetsPage'
import { EndpointDetailPage } from '@/pages/cmdb/EndpointDetailPage'
import { AgentsPage } from '@/pages/gateway/AgentsPage'
import { AgentDetailPage } from '@/pages/gateway/AgentDetailPage'
import { PoliciesPage } from '@/pages/gateway/PoliciesPage'
import { ToolsPage } from '@/pages/gateway/ToolsPage'
import { WorkflowsPage } from '@/pages/gateway/WorkflowsPage'
import { WorkflowCanvasPage } from '@/pages/gateway/WorkflowCanvasPage'
import { NodesPage } from '@/pages/gateway/NodesPage'
import ApprovalsPage from '@/pages/ops/ApprovalsPage'
import { FinOpsPage } from '@/pages/finops/FinOpsPage'
import { MemoryPage } from '@/pages/ops/MemoryPage'
import { PasteEventsPage } from '@/pages/ops/PasteEventsPage'
import { AuditPage } from '@/pages/ops/AuditPage'
import AlertsPage from '@/pages/ops/AlertsPage'
import { AdminPage } from '@/pages/admin/AdminPage'

// B-252 C1: Agent Detail moved to /assets/agents/:id. The old path is kept
// only as a redirect for bookmarks and external links (every in-app link
// points at the new path), carrying ?tab= and any #hash across.
function AgentDetailRedirect() {
  const { id } = useParams<{ id: string }>()
  const { search, hash } = useLocation()
  return <Navigate to={`/assets/agents/${encodeURIComponent(id ?? '')}${search}${hash}`} replace />
}

// B-252: /settings (any ?tab=) now lives at /admin.
function SettingsRedirect() {
  const location = useLocation()
  return <Navigate to={`/admin${location.search}`} replace />
}

export const router = createBrowserRouter([
  {
    path: '/',
    element: <Navigate to="/dashboard" replace />,
  },
  {
    element: <AppShell />,
    children: [
      { path: '/dashboard', element: <DashboardPage /> },
      // B-252 C4: Discover is retired; its list now lives in Assets' Endpoints view.
      { path: '/discover', element: <Navigate to="/assets?kind=endpoint" replace /> },
      { path: '/assets', element: <AssetsPage /> },
      { path: '/assets/agents/:id', element: <AgentDetailPage /> },
      { path: '/assets/endpoints/:id', element: <EndpointDetailPage /> },
      { path: '/gateway/agents', element: <AgentsPage /> },
      { path: '/gateway/agents/:id', element: <AgentDetailRedirect /> },
      { path: '/gateway/policies', element: <PoliciesPage /> },
      { path: '/gateway/tools', element: <ToolsPage /> },
      { path: '/gateway/workflows', element: <WorkflowsPage /> },
      { path: '/gateway/workflows/:id/canvas-preview', element: <WorkflowCanvasPage /> },
      { path: '/gateway/nodes', element: <NodesPage /> },
      { path: '/approvals', element: <ApprovalsPage /> },
      { path: '/finops', element: <FinOpsPage /> },
      { path: '/memory', element: <MemoryPage /> },
      { path: '/paste-events', element: <PasteEventsPage /> },
      { path: '/audit', element: <AuditPage /> },
      { path: '/alerts', element: <AlertsPage /> },
      // B-252: Settings is renamed Admin; old links and bookmarks keep their ?tab=.
      { path: '/admin', element: <AdminPage /> },
      { path: '/settings', element: <SettingsRedirect /> },
      { path: '/profile', element: <ProfilePage /> },
    ],
  },
  // Workspace mode (B-210) -- deliberately its own top-level shell, not
  // nested under AppShell/Admin mode (DESIGN_SYSTEM.md §0: don't blend
  // modes). WorkspaceShell itself is the real access guard (GET
  // /v1/workspaces/mine-driven, not just a hidden nav item) -- a single
  // route with an optional :workspaceId param covers both a bare
  // /workspace (resolves to the user's own first real membership) and
  // /workspace/:workspaceId, instead of two near-duplicate route blocks
  // that a future workspace page could update only one of (code-review
  // finding).
  {
    path: '/workspace/:workspaceId?',
    element: <WorkspaceShell />,
    children: [
      { index: true, element: <WorkspaceOverviewPage /> },
      { path: 'policies', element: <WorkspacePoliciesPage /> },
    ],
  },
  {
    path: '/login',
    element: <LoginPage />,
  },
  {
    path: '/accept-invite',
    element: <AcceptInvitePage />,
  },
  {
    path: '/forgot-password',
    element: <ForgotPasswordPage />,
  },
  {
    path: '/reset-password',
    element: <ResetPasswordPage />,
  },
  {
    path: '/setup',
    element: <SetupWizardPage />,
  },
  {
    path: '*',
    element: <Navigate to="/dashboard" replace />,
  },
])
