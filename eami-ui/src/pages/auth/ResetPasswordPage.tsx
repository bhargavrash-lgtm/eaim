import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Link, useSearchParams } from 'react-router-dom'
import { KeyRound, CheckCircle2, AlertTriangle } from 'lucide-react'
import { apiFetch } from '@/api/client'
import { Card } from '@/components/common/Card'
import { Button } from '@/components/common/Button'
import { Logo } from '@/components/layout/Logo'

// POST /v1/auth/reset-password (eami-api/internal/api/provisioning.go) --
// same undocumented-route apiFetch precedent as AcceptInvitePage.tsx.
const schema = z
  .object({
    newPassword: z.string().min(8, 'Password must be at least 8 characters'),
    confirmPassword: z.string().min(1, 'Confirm your password'),
  })
  .refine((v) => v.newPassword === v.confirmPassword, {
    message: 'Passwords do not match',
    path: ['confirmPassword'],
  })
type FormValues = z.infer<typeof schema>

export function ResetPasswordPage() {
  const [searchParams] = useSearchParams()
  const token = searchParams.get('token') ?? ''
  const [done, setDone] = useState(false)

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
    setError,
  } = useForm<FormValues>({ resolver: zodResolver(schema) })

  async function onSubmit(values: FormValues) {
    try {
      await apiFetch('/v1/auth/reset-password', {
        method: 'POST',
        body: { token, new_password: values.newPassword },
      })
      setDone(true)
    } catch (err) {
      setError('root', {
        message: err instanceof Error ? err.message : 'Could not reset your password',
      })
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 p-4">
      <Card className="w-full max-w-sm rounded-xl border-gray-200 p-8 shadow-sm">
        {!token ? (
          <div className="flex flex-col items-center text-center">
            <AlertTriangle className="h-10 w-10 text-amber-600" />
            <h1 className="mt-3 text-xl font-bold text-gray-900">Reset link is incomplete</h1>
            <p className="mt-2 text-sm text-gray-600">
              This link is missing its reset token.
            </p>
            <Link
              to="/forgot-password"
              className="mt-6 w-full rounded-md border border-gray-300 px-4 py-2 text-center text-sm font-medium text-gray-700 hover:bg-gray-50"
            >
              Request a new link
            </Link>
          </div>
        ) : done ? (
          <div className="flex flex-col items-center text-center">
            <CheckCircle2 className="h-10 w-10 text-green-600" />
            <h1 className="mt-3 text-xl font-bold text-gray-900">Password reset</h1>
            <p className="mt-2 text-sm text-gray-600">
              Your password has been changed. Sign in with your new password.
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
              <KeyRound className="mt-3 h-8 w-8 text-brand-600" />
              <h1 className="mt-3 text-xl font-bold text-gray-900">Set a new password</h1>
            </div>

            <form onSubmit={handleSubmit(onSubmit)} className="space-y-4" noValidate>
              <div>
                <label htmlFor="newPassword" className="block text-sm font-medium text-gray-700">
                  New password
                </label>
                <input
                  id="newPassword"
                  type="password"
                  autoComplete="new-password"
                  {...register('newPassword')}
                  className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
                />
                {errors.newPassword && (
                  <p className="mt-1 text-xs text-red-600">{errors.newPassword.message}</p>
                )}
              </div>

              <div>
                <label htmlFor="confirmPassword" className="block text-sm font-medium text-gray-700">
                  Confirm new password
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
                Reset password
              </Button>
            </form>
          </>
        )}
      </Card>
    </div>
  )
}
