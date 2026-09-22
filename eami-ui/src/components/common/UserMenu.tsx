import { LogOut } from 'lucide-react'
import { useAuthStore } from '@/stores/authStore'
import { Link, useNavigate } from 'react-router-dom'

// Phase 1 of the top-bar retrofit (B-201): the real, working logout
// mechanism, extracted unmodified from Topbar.tsx (already correct on
// Dashboard/Discover/Settings/FinOps/Paste Detection) into its own shared
// component -- so Phase 1's 9-page rollout and Phase 2's later top-bar
// unification share one implementation, instead of Phase 1 hand-copying
// this logic 9 times and Phase 2 replacing it again shortly after.
//
// Deliberately minimal: just the user email + logout button, no bell, no
// search, no breadcrumb -- those are Phase 2's job, added to each of the
// 9 affected pages' existing header without touching their layout/padding.
export function UserMenu() {
  const { user, logout } = useAuthStore()
  const navigate = useNavigate()

  function handleLogout() {
    logout()
    navigate('/login')
  }

  return (
    <div className="flex items-center gap-2">
      <Link to="/profile" className="text-xs text-gray-600 hover:text-gray-900 hover:underline">
        {user?.email}
      </Link>
      <button
        onClick={handleLogout}
        className="rounded p-1 text-gray-500 hover:bg-gray-100"
        aria-label="Log out"
      >
        <LogOut className="h-4 w-4" />
      </button>
    </div>
  )
}
