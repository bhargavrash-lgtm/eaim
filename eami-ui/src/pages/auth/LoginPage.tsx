import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useNavigate } from 'react-router-dom'
import { Shield } from 'lucide-react'
import { api } from '@/api/client'
import { useAuthStore } from '@/stores/authStore'

const loginSchema = z.object({
  email: z.string().email('Invalid email address'),
  password: z.string().min(8, 'Password must be at least 8 characters'),
})

type LoginFormValues = z.infer<typeof loginSchema>

export function LoginPage() {
  const navigate = useNavigate()
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
    navigate('/dashboard', { replace: true })
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 p-4">
      <div className="w-full max-w-sm rounded-xl border border-gray-200 bg-white p-8 shadow-sm">
        <div className="mb-6 flex flex-col items-center">
          <Shield className="h-10 w-10 text-brand-600" />
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

          <button
            type="submit"
            disabled={isSubmitting}
            className="w-full rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-60"
          >
            {isSubmitting ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
      </div>
    </div>
  )
}
