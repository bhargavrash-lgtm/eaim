// ProfilePage.tsx -- self-service account profile (B-197-adjacent
// provisioning brief). GET/PATCH /v1/users/me + POST
// /v1/users/me/change-password (eami-api/internal/api/users.go) -- not in
// api/openapi.yaml yet (Architect-EAMI-owned), same disclosed apiFetch
// escape-hatch precedent as every other undocumented route in this app
// (useTools.ts, useWorkflows.ts, etc.). Reachable from every page via
// UserMenu's own email link, not a sidebar item -- this is a personal
// account page, not one of the six core governance sections the one-spine
// rule's sidebar covers (CLAUDE.md), same precedent SettingsPage already
// sets for account-level (not governance) pages.
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { AppTopBar } from '@/components/layout/AppTopBar'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { Card } from '@/components/common/Card'
import { Button } from '@/components/common/Button'
import { useToast } from '@/components/common/Toast'
import { apiFetch, ApiFetchError } from '@/api/client'
import { useAuthStore } from '@/stores/authStore'

interface WorkspaceMembership {
  workspace_id: string
  workspace_name: string
  role: string
}

interface MeResp {
  id: string
  email: string
  name?: string
  role: string
  org_id: string
  workspaces: WorkspaceMembership[]
}

function Label({ children }: { children: React.ReactNode }) {
  return <label className="block text-sm font-medium text-gray-700">{children}</label>
}
function Input(props: React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...props}
      className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
    />
  )
}
function FieldError({ message }: { message?: string }) {
  if (!message) return null
  return <p className="mt-1 text-xs text-red-600">{message}</p>
}

const nameSchema = z.object({ name: z.string().trim().min(1, 'Name is required') })
type NameFormValues = z.infer<typeof nameSchema>

const passwordSchema = z
  .object({
    currentPassword: z.string().min(1, 'Current password is required'),
    newPassword: z.string().min(8, 'Password must be at least 8 characters'),
    confirmPassword: z.string().min(1, 'Confirm your new password'),
  })
  .refine((v) => v.newPassword === v.confirmPassword, {
    message: 'Passwords do not match',
    path: ['confirmPassword'],
  })
type PasswordFormValues = z.infer<typeof passwordSchema>

export function ProfilePage() {
  const { showToast } = useToast()
  const { setUser } = useAuthStore()
  const [me, setMe] = useState<MeResp | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    apiFetch<MeResp>('/v1/users/me')
      .then((resp) => {
        if (!cancelled) setMe(resp)
      })
      .catch((err) => {
        if (!cancelled) setLoadError(err instanceof Error ? err.message : 'Could not load profile')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const nameForm = useForm<NameFormValues>({ resolver: zodResolver(nameSchema) })
  useEffect(() => {
    if (me) nameForm.reset({ name: me.name ?? '' })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [me])

  async function onSubmitName(values: NameFormValues) {
    try {
      const updated = await apiFetch<{ id: string; email: string; name?: string; role: string; org_id: string }>(
        '/v1/users/me',
        { method: 'PATCH', body: { name: values.name } },
      )
      setMe((prev) => (prev ? { ...prev, name: updated.name } : prev))
      // Keep the top bar's UserMenu (authStore.user) in sync with the new
      // name too -- it reads from the persisted store, not this page's
      // own local state.
      const current = useAuthStore.getState().user
      if (current) setUser({ ...current, name: updated.name })
      showToast('Name updated', { type: 'success' })
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Could not update name', { type: 'error' })
    }
  }

  const passwordForm = useForm<PasswordFormValues>({ resolver: zodResolver(passwordSchema) })

  async function onSubmitPassword(values: PasswordFormValues) {
    try {
      await apiFetch('/v1/users/me/change-password', {
        method: 'POST',
        body: { current_password: values.currentPassword, new_password: values.newPassword },
      })
      passwordForm.reset()
      showToast('Password changed', { type: 'success' })
    } catch (err) {
      const message =
        err instanceof ApiFetchError && err.status === 401
          ? 'Current password is incorrect'
          : err instanceof Error
            ? err.message
            : 'Could not change password'
      passwordForm.setError('currentPassword', { message })
    }
  }

  return (
    <div>
      <AppTopBar breadcrumb={[{ label: 'Profile' }]} />
      <div className="max-w-2xl space-y-6 p-6">
        {loading ? (
          <LoadingSpinner />
        ) : loadError || !me ? (
          <p className="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">
            {loadError ?? 'Could not load profile'}
          </p>
        ) : (
          <>
            <Card className="rounded-lg border-gray-200 p-6">
              <h2 className="text-sm font-semibold text-gray-900">Account</h2>
              <dl className="mt-4 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
                <dt className="text-gray-500">Email</dt>
                <dd className="text-gray-900">{me.email}</dd>
                <dt className="text-gray-500">Role</dt>
                <dd className="text-gray-900 capitalize">{me.role}</dd>
              </dl>
              {me.workspaces.length > 0 && (
                <div className="mt-4">
                  <dt className="text-sm text-gray-500">Workspaces</dt>
                  <ul className="mt-2 space-y-1">
                    {me.workspaces.map((w) => (
                      <li key={w.workspace_id} className="text-sm">
                        <Link to={`/workspace/${w.workspace_id}`} className="text-brand-700 hover:underline">
                          {w.workspace_name}
                        </Link>{' '}
                        <span className="text-gray-500 capitalize">({w.role.replace('workspace_', '')})</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </Card>

            <Card className="rounded-lg border-gray-200 p-6">
              <h2 className="text-sm font-semibold text-gray-900">Display name</h2>
              <form onSubmit={nameForm.handleSubmit(onSubmitName)} className="mt-4 space-y-4" noValidate>
                <div>
                  <Label>Name</Label>
                  <Input {...nameForm.register('name')} />
                  <FieldError message={nameForm.formState.errors.name?.message} />
                </div>
                <Button type="submit" isLoading={nameForm.formState.isSubmitting}>
                  Save name
                </Button>
              </form>
            </Card>

            <Card className="rounded-lg border-gray-200 p-6">
              <h2 className="text-sm font-semibold text-gray-900">Change password</h2>
              <form
                onSubmit={passwordForm.handleSubmit(onSubmitPassword)}
                className="mt-4 space-y-4"
                noValidate
              >
                <div>
                  <Label>Current password</Label>
                  <Input type="password" autoComplete="current-password" {...passwordForm.register('currentPassword')} />
                  <FieldError message={passwordForm.formState.errors.currentPassword?.message} />
                </div>
                <div>
                  <Label>New password</Label>
                  <Input type="password" autoComplete="new-password" {...passwordForm.register('newPassword')} />
                  <FieldError message={passwordForm.formState.errors.newPassword?.message} />
                </div>
                <div>
                  <Label>Confirm new password</Label>
                  <Input type="password" autoComplete="new-password" {...passwordForm.register('confirmPassword')} />
                  <FieldError message={passwordForm.formState.errors.confirmPassword?.message} />
                </div>
                <Button type="submit" isLoading={passwordForm.formState.isSubmitting}>
                  Change password
                </Button>
              </form>
            </Card>
          </>
        )}
      </div>
    </div>
  )
}
