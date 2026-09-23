import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Link, useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { api, apiFetch } from '@/api/client'
import { useAuthStore, consumeRedirectPath } from '@/stores/authStore'
import type { MyWorkspaceMembership } from '@/hooks/useWorkspaces'
import { Logo } from '@/components/layout/Logo'
import { Card } from '@/components/common/Card'
import { Button } from '@/components/common/Button'

const loginSchema = z.object({
  email: z.string().email('Invalid email address'),
  password: z.string().min(8, 'Password must be at least 8 characters'),
})

type LoginFormValues = z.infer<typeof loginSchema>

export function LoginPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { setTokens, setUser } = useAuthStore()

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
    setError,
  } = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
  })

  async function onSubmit(values: LoginFormValues) {
    const { data, error } = await api.POST('/v1/auth/login', {
      body: { email: values.email, password: values.password },
    })

    if (error || !data) {
      setError('root', { message: 'Invalid email or password' })
      return
    }

    setTokens(data.access_token, data.refresh_token)
    if (data.user) {
      setUser(data.user)
    }

    // B-146 AC2: return to the page the user originally tried to reach
    // (captured by authStore.ts at module load), unconditionally first --
    // this must keep winning over the workspace-routing decision below,
    // not just over the plain '/dashboard' default it always beat before.
    const redirectPath = consumeRedirectPath()
    if (redirectPath) {
      navigate(redirectPath, { replace: true })
      return
    }

    // Real post-login routing (closing a real gap found reviewing the
    // Workspaces epic): 'viewer' is the org-role floor -- read-only
    // everywhere in Admin mode, no create/edit/delete capability anywhere
    // -- so a viewer who ALSO has a real workspace membership has no real
    // reason to land on /dashboard; route them straight to their
    // workspace instead. Any other org role (admin/operator/approver)
    // keeps the existing /dashboard default, completely unchanged --
    // this check only ever adds one extra request (GET
    // /v1/workspaces/mine) for viewers, never for anyone else. A viewer
    // with zero memberships (a real, occurring case -- any low-privilege
    // user not yet added to a workspace) falls through to the same
    // unchanged /dashboard default below, same as today.
    if (data.user?.role === 'viewer') {
      try {
        const mine = await apiFetch<{ data: MyWorkspaceMembership[] }>('/v1/workspaces/mine')
        // Code-review finding: prime useMyWorkspaces' own cache (same
        // ['workspaces', 'mine'] key, hooks/useWorkspaces.ts) with the
        // response this fetch already has, so WorkspaceShell's immediate
        // next render doesn't re-issue the identical request against a
        // cold cache -- this is a real request made once, not fetched
        // twice for one navigation.
        queryClient.setQueryData(['workspaces', 'mine'], mine)
        if (mine.data.length > 0) {
          // No specific id -- WorkspaceShell itself resolves to the
          // user's own first real membership, and its already-built
          // in-shell switcher covers the genuinely-more-than-one case
          // (DESIGN_SYSTEM.md §7.5's own real-but-restricted selector),
          // so this doubles as the "minimal chooser" without a second
          // dedicated UI.
          navigate('/workspace', { replace: true })
          return
        }
      } catch {
        // Fall through to the unchanged /dashboard default below --
        // never block a successful login on this being unreachable.
      }
    }

    navigate('/dashboard', { replace: true })
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 p-4">
      <Card className="w-full max-w-sm rounded-xl border-gray-200 p-8 shadow-sm">
        <div className="mb-6 flex flex-col items-center">
          <Logo variant="full" className="h-8 w-auto" />
          <h1 className="mt-3 text-xl font-bold text-gray-900">Sign in to EAMI</h1>
          <p className="mt-1 text-sm text-gray-500">Enterprise AI Governance Platform</p>
        </div>

        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4" noValidate>
          <div>
            <label htmlFor="email" className="block text-sm font-medium text-gray-700">
              Email
            </label>
            <input
              id="email"
              type="email"
              autoComplete="email"
              {...register('email')}
              className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
            />
            {errors.email && (
              <p className="mt-1 text-xs text-red-600">{errors.email.message}</p>
            )}
          </div>

          <div>
            <label htmlFor="password" className="block text-sm font-medium text-gray-700">
              Password
            </label>
            <input
              id="password"
              type="password"
              autoComplete="current-password"
              {...register('password')}
              className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
            />
            {errors.password && (
              <p className="mt-1 text-xs text-red-600">{errors.password.message}</p>
            )}
          </div>

          {errors.root && (
            <p className="rounded-md bg-red-50 px-3 py-2 text-xs text-red-700">
              {errors.root.message}
            </p>
          )}

          <Button type="submit" isLoading={isSubmitting} className="w-full">
            Sign in
          </Button>

          <Link
            to="/forgot-password"
            className="block text-center text-xs text-gray-500 hover:text-gray-700 hover:underline"
          >
            Forgot your password?
          </Link>
        </form>
      </Card>
    </div>
  )
}
