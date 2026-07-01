import { createBrowserRouter, Navigate } from 'react-router-dom'
import { AppShell } from '@/components/layout/AppShell'
import { LoginPage } from '@/pages/auth/LoginPage'
import { DashboardPage } from '@/pages/dashboard/DashboardPage'
import { DiscoverPage } from '@/pages/discover/DiscoverPage'
import { AgentsPage } from '@/pages/gateway/AgentsPage'
import { PoliciesPage } from '@/pages/gateway/PoliciesPage'
import { ToolsPage } from '@/pages/gateway/ToolsPage'
import { NodesPage } from '@/pages/gateway/NodesPage'
import ApprovalsPage from '@/pages/ops/ApprovalsPage'
import { FinOpsPage } from '@/pages/finops/FinOpsPage'
import { MemoryPage } from '@/pages/ops/MemoryPage'
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
      { path: '/gateway/policies', element: <PoliciesPage /> },
      { path: '/gateway/tools', element: <ToolsPage /> },
      { path: '/gateway/nodes', element: <NodesPage /> },
      { path: '/approvals', element: <ApprovalsPage /> },
      { path: '/finops', element: <FinOpsPage /> },
      { path: '/memory', element: <MemoryPage /> },
      { path: '/audit', element: <AuditPage /> },
      { path: '/alerts', element: <AlertsPage /> },
      { path: '/settings', element: <SettingsPage /> },
    ],
  },
  {
    path: '/login',
    element: <LoginPage />,
  },
  {
    path: '*',
    element: <Navigate to="/dashboard" replace />,
  },
])
