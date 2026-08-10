import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Navigate, Link } from 'react-router-dom'
import { KeyRound, Building2, CheckCircle2 } from 'lucide-react'
import { apiFetch, ApiFetchError } from '@/api/client'

// Mirrors eami-api/internal/api/bootstrap.go's SetupStatusResp/
// BootstrapResponse shapes -- these three routes are undocumented in
// api/openapi.yaml (Architect-EAMI-owned), same precedent as B-038/B-045's
// apiFetch escape hatch.
interface SetupStatusResp {
  configured: boolean
}
interface BootstrapResp {
  org_id: string
  org_name: string
  admin_email: string
}

const tokenSchema = z.object({
  setupToken: z.string().min(1, 'Setup token is required'),
})
type TokenFormValues = z.infer<typeof tokenSchema>

const bootstrapSchema = z
  .object({
    orgName: z.string().min(1, 'Organization name is required'),
    adminEmail: z.string().email('Invalid email address'),
    adminPassword: z.string().min(8, 'Password must be at least 8 characters'),
    confirmPassword: z.string().min(1, 'Confirm your password'),
  })
  .refine((v) => v.adminPassword === v.confirmPassword, {
    message: 'Passwords do not match',
    path: ['confirmPassword'],
  })
type BootstrapFormValues = z.infer<typeof bootstrapSchema>

type Stage = 'loading' | 'token' | 'form' | 'success' | 'already-configured'

export function SetupWizardPage() {
  const [stage, setStage] = useState<Stage>('loading')
  const [setupToken, setSetupToken] = useState('')
  const [result, setResult] = useState<BootstrapResp | null>(null)

  // On mount: an already-configured appliance shows the normal login page,
  // not the wizard -- this is what makes the wizard "unreachable" after
  // setup completes, without needing a token just to find that out.
  useEffect(() => {
    let cancelled = false
    apiFetch<SetupStatusResp>('/v1/setup/status')
      .then((status) => {
        if (cancelled) return
        setStage(status.configured ? 'already-configured' : 'token')
      })
      .catch(() => {
        if (!cancelled) setStage('token') // fail open to the token gate, not a blank page
      })
    return () => {
      cancelled = true
    }
  }, [])

  const tokenForm = useForm<TokenFormValues>({ resolver: zodResolver(tokenSchema) })
  const bootstrapForm = useForm<BootstrapFormValues>({ resolver: zodResolver(bootstrapSchema) })

  async function onSubmitToken(values: TokenFormValues) {
    try {
      // Read-only check -- does not consume the token. The atomic,
      // single-use consumption happens only in onSubmitBootstrap below.
      await apiFetch<void>('/v1/setup/token/validate', {
        method: 'POST',
        body: { setup_token: values.setupToken },
      })
      setSetupToken(values.setupToken)
      setStage('form')
    } catch (err) {
      tokenForm.setError('root', {
        message: err instanceof Error ? err.message : 'Invalid setup token',
      })
    }
  }

  async function onSubmitBootstrap(values: BootstrapFormValues) {
    try {
      const resp = await apiFetch<BootstrapResp>('/v1/setup/bootstrap', {
        method: 'POST',
        body: {
          setup_token: setupToken,
          org_name: values.orgName,
          admin_email: values.adminEmail,
          admin_password: values.adminPassword,
        },
      })
      setResult(resp)
      setStage('success')
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Setup failed'
      // Only a dead token (401 -- expired/already used/never existed) or
      // the appliance having been configured by someone else in the
      // meantime (409) can never succeed by retrying this same form -- for
      // those, send the admin back to the token gate. Every other failure
      // (validation error, transient 500, network blip) leaves the token
      // itself still valid -- Bootstrap's transaction rolls back and never
      // consumes it on any failure path -- so the org/admin details the
      // admin already typed are kept and they can just fix the problem and
      // resubmit, instead of losing everything and having to go relocate
      // the 64-character console token again.
      if (err instanceof ApiFetchError && (err.status === 401 || err.status === 409)) {
        tokenForm.setError('root', { message })
        setStage('token')
      } else {
        bootstrapForm.setError('root', { message })
      }
    }
  }

  if (stage === 'loading') {
    return <div className="flex min-h-screen items-center justify-center bg-gray-50" />
  }

  if (stage === 'already-configured') {
    return <Navigate to="/login" replace />
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 p-4">
      <div className="w-full max-w-sm rounded-xl border border-gray-200 bg-white p-8 shadow-sm">
        {stage === 'token' && (
          <>
            <div className="mb-6 flex flex-col items-center">
              <KeyRound className="h-10 w-10 text-brand-600" />
              <h1 className="mt-3 text-xl font-bold text-gray-900">EAMI first-boot setup</h1>
              <p className="mt-1 text-center text-sm text-gray-500">
                Enter the setup token shown on this appliance&apos;s console.
              </p>
            </div>

            <form onSubmit={tokenForm.handleSubmit(onSubmitToken)} className="space-y-4" noValidate>
              <div>
                <label htmlFor="setupToken" className="block text-sm font-medium text-gray-700">
                  Setup token
                </label>
                <input
                  id="setupToken"
                  type="text"
                  autoComplete="off"
                  spellCheck={false}
                  {...tokenForm.register('setupToken')}
                  className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 font-mono text-sm shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
                />
                {tokenForm.formState.errors.setupToken && (
                  <p className="mt-1 text-xs text-red-600">
                    {tokenForm.formState.errors.setupToken.message}
                  </p>
                )}
              </div>

              {tokenForm.formState.errors.root && (
                <p className="rounded-md bg-red-50 px-3 py-2 text-xs text-red-700">
                  {tokenForm.formState.errors.root.message}
                </p>
              )}

              <button
                type="submit"
                disabled={tokenForm.formState.isSubmitting}
                className="w-full rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-60"
              >
                {tokenForm.formState.isSubmitting ? 'Checking…' : 'Continue'}
              </button>
            </form>
          </>
        )}

        {stage === 'form' && (
          <>
            <div className="mb-6 flex flex-col items-center">
              <Building2 className="h-10 w-10 text-brand-600" />
              <h1 className="mt-3 text-xl font-bold text-gray-900">Create your organization</h1>
              <p className="mt-1 text-sm text-gray-500">This creates the first admin account.</p>
            </div>

            <form
              onSubmit={bootstrapForm.handleSubmit(onSubmitBootstrap)}
              className="space-y-4"
              noValidate
            >
              <div>
                <label htmlFor="orgName" className="block text-sm font-medium text-gray-700">
                  Organization name
                </label>
                <input
                  id="orgName"
                  type="text"
                  {...bootstrapForm.register('orgName')}
                  className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
                />
                {bootstrapForm.formState.errors.orgName && (
                  <p className="mt-1 text-xs text-red-600">
                    {bootstrapForm.formState.errors.orgName.message}
                  </p>
                )}
              </div>

              <div>
                <label htmlFor="adminEmail" className="block text-sm font-medium text-gray-700">
                  Admin email
                </label>
                <input
                  id="adminEmail"
                  type="email"
                  autoComplete="email"
                  {...bootstrapForm.register('adminEmail')}
                  className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
                />
                {bootstrapForm.formState.errors.adminEmail && (
                  <p className="mt-1 text-xs text-red-600">
                    {bootstrapForm.formState.errors.adminEmail.message}
                  </p>
                )}
              </div>

              <div>
                <label htmlFor="adminPassword" className="block text-sm font-medium text-gray-700">
                  Admin password
                </label>
                <input
                  id="adminPassword"
                  type="password"
                  autoComplete="new-password"
                  {...bootstrapForm.register('adminPassword')}
                  className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
                />
                {bootstrapForm.formState.errors.adminPassword && (
                  <p className="mt-1 text-xs text-red-600">
                    {bootstrapForm.formState.errors.adminPassword.message}
                  </p>
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
                  {...bootstrapForm.register('confirmPassword')}
                  className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
                />
                {bootstrapForm.formState.errors.confirmPassword && (
                  <p className="mt-1 text-xs text-red-600">
                    {bootstrapForm.formState.errors.confirmPassword.message}
                  </p>
                )}
              </div>

              {bootstrapForm.formState.errors.root && (
                <p className="rounded-md bg-red-50 px-3 py-2 text-xs text-red-700">
                  {bootstrapForm.formState.errors.root.message}
                </p>
              )}

              <button
                type="submit"
                disabled={bootstrapForm.formState.isSubmitting}
                className="w-full rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-60"
              >
                {bootstrapForm.formState.isSubmitting ? 'Creating…' : 'Create organization'}
              </button>
            </form>
          </>
        )}

        {stage === 'success' && result && (
          <div className="flex flex-col items-center text-center">
            <CheckCircle2 className="h-10 w-10 text-green-600" />
            <h1 className="mt-3 text-xl font-bold text-gray-900">Setup complete</h1>
            <p className="mt-2 text-sm text-gray-600">
              <span className="font-medium">{result.org_name}</span> is ready. Sign in with{' '}
              <span className="font-medium">{result.admin_email}</span> and the password you just
              set.
            </p>
            <Link
              to="/login"
              className="mt-6 w-full rounded-md bg-brand-600 px-4 py-2 text-center text-sm font-medium text-white hover:bg-brand-700"
            >
              Go to sign in
            </Link>
          </div>
        )}
      </div>
    </div>
  )
}
