import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Link } from 'react-router-dom'
import { MailQuestion, CheckCircle2 } from 'lucide-react'
import { apiFetch } from '@/api/client'
import { Card } from '@/components/common/Card'
import { Button } from '@/components/common/Button'
import { Logo } from '@/components/layout/Logo'

// POST /v1/auth/request-reset always returns 200 regardless of whether the
// account exists (anti-enumeration -- eami-api/internal/api/provisioning.go)
// -- this page's own copy is deliberately worded to match that honestly:
// it never claims an email was sent (no real email delivery exists in this
// codebase, confirmed before building this), only that a request was
// recorded. Same undocumented-route apiFetch precedent as
// AcceptInvitePage.tsx.
const schema = z.object({
  email: z.string().email('Invalid email address'),
})
type FormValues = z.infer<typeof schema>

export function ForgotPasswordPage() {
  const [submitted, setSubmitted] = useState(false)

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({ resolver: zodResolver(schema) })

  async function onSubmit(values: FormValues) {
    // Always the same outcome regardless of the response -- the backend's
    // anti-enumeration guarantee is only real if this page never branches
    // on whether the account existed either.
    try {
      await apiFetch('/v1/auth/request-reset', { method: 'POST', body: { email: values.email } })
    } catch {
      // Deliberately ignored -- see comment above.
    }
    setSubmitted(true)
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 p-4">
      <Card className="w-full max-w-sm rounded-xl border-gray-200 p-8 shadow-sm">
        {submitted ? (
          <div className="flex flex-col items-center text-center">
            <CheckCircle2 className="h-10 w-10 text-green-600" />
            <h1 className="mt-3 text-xl font-bold text-gray-900">Request received</h1>
            <p className="mt-2 text-sm text-gray-600">
              If an account exists for that email address, a password reset has been requested.
              This deployment doesn&apos;t send email -- ask your administrator to retrieve the
              reset link for you.
            </p>
            <Link
              to="/login"
              className="mt-6 w-full rounded-md bg-brand-600 px-4 py-2 text-center text-sm font-medium text-white hover:bg-brand-700"
            >
              Back to sign in
            </Link>
          </div>
        ) : (
          <>
            <div className="mb-6 flex flex-col items-center">
              <Logo variant="full" className="h-8 w-auto" />
              <MailQuestion className="mt-3 h-8 w-8 text-brand-600" />
              <h1 className="mt-3 text-xl font-bold text-gray-900">Reset your password</h1>
              <p className="mt-1 text-center text-sm text-gray-500">
                Enter your account email. Your administrator will need to hand you the reset
                link -- this deployment has no email delivery configured.
              </p>
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

              <Button type="submit" isLoading={isSubmitting} className="w-full">
                Request reset link
              </Button>

              <Link
                to="/login"
                className="block text-center text-xs text-gray-500 hover:text-gray-700 hover:underline"
              >
                Back to sign in
              </Link>
            </form>
          </>
        )}
      </Card>
    </div>
  )
}
