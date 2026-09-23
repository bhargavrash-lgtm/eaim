// WorkspaceShell.tsx -- B-210: Workspace mode's own shell, deliberately
// separate from AppShell/Sidebar (Admin mode). DESIGN_SYSTEM.md §0: "Do
// not blend these [modes]." -- Workspace mode is "outcome-oriented,
// restricted, calm, few numbers," not Admin's dense 12-item, 5-group
// sidebar. Built against the live Layer4-WorkspaceUserMode.dc.html canvas
// mockup, read directly, not from memory: a slim ~200px sidebar (logo,
// workspace context, Overview/Our Policies nav, user footer) + a plain
// top bar -- structurally its own thing, not a variant of AppShell.
//
// The real access gate (AC3: a user with zero workspace memberships
// cannot reach this UI at all) is GET /v1/workspaces/mine, the only
// membership-scoped signal -- GetWorkspace/ListWorkspaces are NOT
// membership-gated (router.go's own comment: "names/existence are
// organizational metadata"), so checking those instead would be a real
// fail-open mistake, not just a UX nicety gone wrong. This is a real,
// data-driven guard (redirects if /mine is empty, or if the URL's
// workspaceId isn't among the user's own real memberships), not just a
// hidden nav item -- and it's defense-in-depth on top of, never a
// substitute for, requireWorkspaceRole's own independent server-side
// enforcement on every actual data call underneath it.
import { useEffect } from 'react'
import { Outlet, useNavigate, useParams, Link, Navigate } from 'react-router-dom'
import { LayoutDashboard, ShieldCheck, LogOut } from 'lucide-react'
import { Logo } from './Logo'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { ToastProvider } from '@/components/common/Toast'
import { useAuthStore } from '@/stores/authStore'
import { useMyWorkspaces } from '@/hooks/useWorkspaces'

function WorkspaceSidebar({ workspaceId }: { workspaceId: string }) {
  const { data } = useMyWorkspaces()
  const memberships = data?.data ?? []
  const current = memberships.find((m) => m.workspace_id === workspaceId)
  const { user, logout } = useAuthStore()
  const navigate = useNavigate()

  return (
    <div className="w-[200px] flex-shrink-0 bg-white border-r border-gray-200 p-3.5 box-border flex flex-col gap-6">
      <Link to="/dashboard" className="px-1.5">
        <Logo variant="full" className="h-5 w-auto" />
      </Link>

      {/* Workspace context: non-interactive if exactly one real membership,
          a real (still not free-switching) selector restricted to the
          user's own memberships if they genuinely have more than one --
          DESIGN_SYSTEM.md §7.5, never a free multi-workspace browser. */}
      {memberships.length > 1 ? (
        <select
          value={workspaceId}
          onChange={(e) => navigate(`/workspace/${e.target.value}`)}
          aria-label="Switch workspace"
          className="flex items-center gap-2 bg-brand-50 text-brand-700 text-[11.5px] font-semibold rounded-lg px-3 py-2 border-none focus:outline-none focus:ring-1 focus:ring-brand-500"
        >
          {memberships.map((m) => (
            <option key={m.workspace_id} value={m.workspace_id}>{m.workspace_name}</option>
          ))}
        </select>
      ) : (
        <div className="flex items-center gap-2 bg-brand-50 rounded-lg px-3 py-2.5">
          <ShieldCheck className="h-3.5 w-3.5 text-brand-500" />
          <span className="text-[11.5px] font-semibold text-brand-700">{current?.workspace_name ?? '...'}</span>
        </div>
      )}

      <nav className="flex flex-col gap-0.5">
        <Link
          to={`/workspace/${workspaceId}`}
          className="flex items-center gap-2.5 text-[13px] font-semibold text-white bg-brand-500 rounded-md px-2.5 py-2"
        >
          <LayoutDashboard className="h-4 w-4" />
          Overview
        </Link>
        <Link
          to={`/workspace/${workspaceId}/policies`}
          className="flex items-center gap-2.5 text-[13px] font-medium text-ink-muted rounded-md px-2.5 py-2 hover:bg-gray-50"
        >
          <ShieldCheck className="h-4 w-4" />
          Our Policies
        </Link>
      </nav>

      <div className="mt-auto flex items-center gap-2.5 pt-4 border-t border-gray-100">
        <div className="w-[30px] h-[30px] rounded-full bg-brand-50 flex items-center justify-center text-[11px] font-bold text-brand-500">
          {(user?.name ?? user?.email ?? '?').slice(0, 2).toUpperCase()}
        </div>
        <span className="text-[11.5px] font-semibold text-ink-muted truncate flex-1">{user?.name ?? user?.email}</span>
        <button onClick={() => { logout(); navigate('/login') }} aria-label="Log out" className="text-gray-400 hover:text-gray-600">
          <LogOut className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  )
}

export function WorkspaceShell() {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated)
  const { workspaceId } = useParams<{ workspaceId?: string }>()
  const navigate = useNavigate()
  const { data, isLoading } = useMyWorkspaces()

  const memberships = data?.data ?? []
  const isMember = !!workspaceId && memberships.some((m) => m.workspace_id === workspaceId)

  useEffect(() => {
    if (isLoading) return
    if (memberships.length === 0) {
      // AC3: zero real memberships -- this UI is unreachable, full stop.
      navigate('/dashboard', { replace: true })
      return
    }
    if (!workspaceId) {
      // /workspace with no id -- resolve to the user's own first real
      // membership (never an arbitrary/guessable workspace).
      navigate(`/workspace/${memberships[0].workspace_id}`, { replace: true })
      return
    }
    if (!isMember) {
      // The URL names a workspace the user has no real membership row
      // for -- redirect rather than render, even though the actual data
      // calls underneath are independently 403'd server-side regardless.
      navigate(`/workspace/${memberships[0].workspace_id}`, { replace: true })
    }
  }, [isLoading, memberships, workspaceId, isMember, navigate])

  if (!isAuthenticated) return <Navigate to="/login" replace />
  if (isLoading || !workspaceId || !isMember) {
    return <div className="flex h-screen items-center justify-center bg-gray-50"><LoadingSpinner /></div>
  }

  return (
    <ToastProvider>
      <div className="flex h-screen overflow-hidden bg-[#F7F8FB]">
        <WorkspaceSidebar workspaceId={workspaceId} />
        <div className="flex flex-1 flex-col overflow-hidden">
          <main className="flex-1 overflow-y-auto">
            <Outlet context={{ workspaceId }} />
          </main>
        </div>
      </div>
    </ToastProvider>
  )
}
