import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Link, useSearchParams } from 'react-router-dom'
import { UserPlus, CheckCircle2, AlertTriangle } from 'lucide-react'
import { apiFetch } from '@/api/client'
import { Card } from '@/components/common/Card'
import { Button } from '@/components/common/Button'
import { Logo } from '@/components/layout/Logo'

// The real, working /accept-invite page -- until this page existed, it was
// a dead link: InviteUser (eami-api/internal/api/users.go) generated a real
// invite_link, but nothing ever consumed it, so an invited user could never
// actually set a password and log in. POST /v1/auth/accept-invite (new,
// see eami-api/internal/api/provisioning.go) isn't in api/openapi.yaml yet
// (Architect-EAMI-owned) -- same disclosed apiFetch-escape-hatch precedent
// SetupWizardPage.tsx already established for its own 3 undocumented routes.
interface AcceptInviteResp {
  email: string
}

const schema = z
  .object({
    password: z.string().min(8, 'Password must be at least 8 characters'),
    confirmPassword: z.string().min(1, 'Confirm your password'),
  })
  .refine((v) => v.password === v.confirmPassword, {
    message: 'Passwords do not match',
    path: ['confirmPassword'],
  })
type FormValues = z.infer<typeof schema>

export function AcceptInvitePage() {
  const [searchParams] = useSearchParams()
  const token = searchParams.get('token') ?? ''
  const [accepted, setAccepted] = useState<AcceptInviteResp | null>(null)

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
    setError,
  } = useForm<FormValues>({ resolver: zodResolver(schema) })

  async function onSubmit(values: FormValues) {
    try {
      const resp = await apiFetch<AcceptInviteResp>('/v1/auth/accept-invite', {
        method: 'POST',
        body: { token, password: values.password },
      })
      setAccepted(resp)
    } catch (err) {
      setError('root', {
        message: err instanceof Error ? err.message : 'Could not accept this invite',
      })
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 p-4">
      <Card className="w-full max-w-sm rounded-xl border-gray-200 p-8 shadow-sm">
        {!token ? (
          <div className="flex flex-col items-center text-center">
            <AlertTriangle className="h-10 w-10 text-amber-600" />
            <h1 className="mt-3 text-xl font-bold text-gray-900">Invite link is incomplete</h1>
            <p className="mt-2 text-sm text-gray-600">
              This link is missing its invite token. Ask whoever invited you to send the link
              again.
            </p>
            <Link
              to="/login"
              className="mt-6 w-full rounded-md border border-gray-300 px-4 py-2 text-center text-sm font-medium text-gray-700 hover:bg-gray-50"
            >
              Back to sign in
            </Link>
          </div>
        ) : accepted ? (
          <div className="flex flex-col items-center text-center">
            <CheckCircle2 className="h-10 w-10 text-green-600" />
            <h1 className="mt-3 text-xl font-bold text-gray-900">You&apos;re all set</h1>
            <p className="mt-2 text-sm text-gray-600">
              Your password has been set for <span className="font-medium">{accepted.email}</span>
              . Sign in to continue.
            </p>
            <Link
              to="/login"
              className="mt-6 w-full rounded-md bg-brand-600 px-4 py-2 text-center text-sm font-medium text-white hover:bg-brand-700"
            >
              Go to sign in
            </Link>
          </div>
        ) : (
          <>
            <div className="mb-6 flex flex-col items-center">
              <Logo variant="full" className="h-8 w-auto" />
              <UserPlus className="mt-3 h-8 w-8 text-brand-600" />
              <h1 className="mt-3 text-xl font-bold text-gray-900">Accept your invite</h1>
              <p className="mt-1 text-center text-sm text-gray-500">
                Set a password to activate your EAMI account.
              </p>
            </div>

            <form onSubmit={handleSubmit(onSubmit)} className="space-y-4" noValidate>
              <div>
                <label htmlFor="password" className="block text-sm font-medium text-gray-700">
                  Password
                </label>
                <input
                  id="password"
                  type="password"
                  autoComplete="new-password"
                  {...register('password')}
                  className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
                />
                {errors.password && (
                  <p className="mt-1 text-xs text-red-600">{errors.password.message}</p>
                )}
              </div>

              <div>
                <label htmlFor="confirmPassword" className="block text-sm font-medium text-gray-700">
                  Confirm password
                </label>
                <input
                  id="confirmPassword"
                  type="password"
                  autoComplete="new-password"
                  {...register('confirmPassword')}
                  className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
                />
                {errors.confirmPassword && (
                  <p className="mt-1 text-xs text-red-600">{errors.confirmPassword.message}</p>
                )}
              </div>

              {errors.root && (
                <p className="rounded-md bg-red-50 px-3 py-2 text-xs text-red-700">
                  {errors.root.message}
                </p>
              )}

              <Button type="submit" isLoading={isSubmitting} className="w-full">
                Set password &amp; activate account
              </Button>
            </form>
          </>
        )}
      </Card>
    </div>
  )
}
