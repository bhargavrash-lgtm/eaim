import { createBrowserRouter, Navigate } from 'react-router-dom'
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
import { DiscoverPage } from '@/pages/discover/DiscoverPage'
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
import { SettingsPage } from '@/pages/settings/SettingsPage'

export const router = createBrowserRouter([
  {
    path: '/',
    element: <Navigate to="/dashboard" replace />,
  },
  {
    element: <AppShell />,
    children: [
      { path: '/dashboard', element: <DashboardPage /> },
      { path: '/discover', element: <DiscoverPage /> },
      { path: '/gateway/agents', element: <AgentsPage /> },
      { path: '/gateway/agents/:id', element: <AgentDetailPage /> },
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
      { path: '/settings', element: <SettingsPage /> },
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
