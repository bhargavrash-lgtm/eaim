import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Copy, Check, Eye, EyeOff, RefreshCw } from 'lucide-react'
import { Topbar } from '@/components/layout/Topbar'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { EmptyState } from '@/components/common/EmptyState'
import { ConfirmDialog } from '@/components/common/ConfirmDialog'
import { useAuthStore } from '@/stores/authStore'
import { useOrgSettings, useUpdateOrgSettings } from '@/hooks/useOrgSettings'
import { useUsers, useInviteUser, useChangeUserRole, useRevokeUser, type UserRole } from '@/hooks/useUsers'
import { useNotificationSettings, useUpdateNotificationSettings, useTestNotification } from '@/hooks/useNotificationSettings'
import { useApiKeys, useCreateApiKey, useRevokeApiKey } from '@/hooks/useApiKeys'
import type { components } from '@/api/schema'

type OrgUser = components['schemas']['OrgUser']
type ApiKey = components['schemas']['ApiKey']

// ── Shared UI primitives ──────────────────────────────────────────────────────

function Label({ children }: { children: React.ReactNode }) {
  return <label className="block text-sm font-medium text-gray-700 mb-1">{children}</label>
}

function Input(props: React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...props}
      className={`block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm
        focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500
        disabled:bg-gray-100 disabled:text-gray-400 ${props.className ?? ''}`}
    />
  )
}

function Select(props: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      {...props}
      className={`block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm
        focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 ${props.className ?? ''}`}
    />
  )
}

function FieldError({ message }: { message?: string }) {
  if (!message) return null
  return <p className="mt-1 text-xs text-red-600">{message}</p>
}

function SaveButton({ isLoading, label = 'Save changes' }: { isLoading?: boolean; label?: string }) {
  return (
    <button
      type="submit"
      disabled={isLoading}
      className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-60"
    >
      {isLoading && <RefreshCw className="h-3.5 w-3.5 animate-spin" />}
      {label}
    </button>
  )
}

function Toast({ message, type }: { message: string; type: 'success' | 'error' }) {
  return (
    <div className={`rounded-md px-4 py-2 text-sm font-medium ${type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800'}`}>
      {message}
    </div>
  )
}

function CopyButton({ value }: { value: string }) {
  const [copied, setCopied] = useState(false)
  function copy() {
    navigator.clipboard.writeText(value).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }
  return (
    <button onClick={copy} className="rounded p-1 text-gray-400 hover:text-gray-700" title="Copy">
      {copied ? <Check className="h-4 w-4 text-green-600" /> : <Copy className="h-4 w-4" />}
    </button>
  )
}

function RoleBadge({ role }: { role: string }) {
  const styles: Record<string, string> = {
    admin: 'bg-red-100 text-red-800',
    operator: 'bg-brand-100 text-brand-800',
    approver: 'bg-amber-100 text-amber-800',
    viewer: 'bg-gray-100 text-gray-700',
  }
  return (
    <span className={`inline-flex rounded-full px-2 py-0.5 text-xs font-medium capitalize ${styles[role] ?? 'bg-gray-100 text-gray-700'}`}>
      {role}
    </span>
  )
}

// ── Timezone list (common IANA zones) ─────────────────────────────────────────
const TIMEZONES = [
  'UTC', 'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles',
  'America/Toronto', 'America/Vancouver', 'Europe/London', 'Europe/Paris', 'Europe/Berlin',
  'Europe/Amsterdam', 'Europe/Stockholm', 'Europe/Zurich', 'Asia/Tokyo', 'Asia/Shanghai',
  'Asia/Singapore', 'Asia/Kolkata', 'Asia/Dubai', 'Australia/Sydney', 'Pacific/Auckland',
]

// ── Tab 1: Org ─────────────────────────────────────────────────────────────────

const orgSchema = z.object({
  name: z.string().min(1, 'Organisation name is required'),
  timezone: z.string().min(1, 'Timezone is required'),
  default_risk_tier: z.enum(['low', 'medium', 'high', 'critical']),
})
type OrgFormValues = z.infer<typeof orgSchema>

function OrgTab() {
  const { data: settings, isLoading } = useOrgSettings()
  const update = useUpdateOrgSettings()
  const [toast, setToast] = useState<{ msg: string; type: 'success' | 'error' } | null>(null)

  const { register, handleSubmit, reset, formState: { errors } } = useForm<OrgFormValues>({
    resolver: zodResolver(orgSchema),
    defaultValues: { name: '', timezone: 'UTC', default_risk_tier: 'low' },
  })

  useEffect(() => {
    if (settings) {
      reset({
        name: settings.name,
        timezone: settings.timezone,
        default_risk_tier: settings.default_risk_tier,
      })
    }
  }, [settings, reset])

  async function onSubmit(values: OrgFormValues) {
    try {
      await update.mutateAsync(values)
      setToast({ msg: 'Settings saved.', type: 'success' })
    } catch {
      setToast({ msg: 'Failed to save settings.', type: 'error' })
    }
    setTimeout(() => setToast(null), 3000)
  }

  if (isLoading) return <div className="flex justify-center py-16"><LoadingSpinner /></div>

  return (
    <form onSubmit={handleSubmit(onSubmit)} className="max-w-lg space-y-5">
      <div>
        <Label>Organisation name</Label>
        <Input {...register('name')} />
        <FieldError message={errors.name?.message} />
      </div>

      <div>
        <Label>Slug (read-only)</Label>
        <Input value={settings?.slug ?? ''} disabled readOnly />
        <p className="mt-1 text-xs text-gray-400">Cannot be changed after org creation.</p>
      </div>

      <div>
        <Label>Plan</Label>
        <Input value={settings?.plan ?? ''} disabled readOnly className="capitalize" />
      </div>

      <div>
        <Label>Timezone</Label>
        <Select {...register('timezone')}>
          {TIMEZONES.map((tz) => <option key={tz} value={tz}>{tz}</option>)}
        </Select>
        <FieldError message={errors.timezone?.message} />
      </div>

      <div>
        <Label>Default risk tier for new agents</Label>
        <Select {...register('default_risk_tier')}>
          <option value="low">Low</option>
          <option value="medium">Medium</option>
          <option value="high">High</option>
          <option value="critical">Critical</option>
        </Select>
        <FieldError message={errors.default_risk_tier?.message} />
      </div>

      <div className="flex items-center gap-4">
        <SaveButton isLoading={update.isPending} />
        {toast && <Toast message={toast.msg} type={toast.type} />}
      </div>
    </form>
  )
}

// ── Tab 2: Users ───────────────────────────────────────────────────────────────

const inviteSchema = z.object({
  email: z.string().email('Valid email required'),
  role: z.enum(['admin', 'operator', 'approver', 'viewer']),
})
type InviteFormValues = z.infer<typeof inviteSchema>

function UsersTab() {
  const currentUser = useAuthStore((s) => s.user)
  const { data, isLoading } = useUsers()
  const invite = useInviteUser()
  const changeRole = useChangeUserRole()
  const revoke = useRevokeUser()

  const [showInvite, setShowInvite] = useState(false)
  const [inviteLink, setInviteLink] = useState<string | null>(null)
  const [revokeTarget, setRevokeTarget] = useState<OrgUser | null>(null)

  const { register, handleSubmit, reset, formState: { errors } } = useForm<InviteFormValues>({
    resolver: zodResolver(inviteSchema),
    defaultValues: { email: '', role: 'viewer' },
  })

  async function onInvite(values: InviteFormValues) {
    const result = await invite.mutateAsync({ email: values.email, role: values.role })
    if (result?.invite_link) setInviteLink(result.invite_link)
    reset()
  }

  const users: OrgUser[] = data?.data ?? []

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <p className="text-sm text-gray-500">{users.length} member{users.length !== 1 ? 's' : ''}</p>
        <button
          onClick={() => { setShowInvite(true); setInviteLink(null) }}
          className="rounded-md bg-brand-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-brand-700"
        >
          Invite user
        </button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-8"><LoadingSpinner /></div>
      ) : users.length === 0 ? (
        <EmptyState title="No users yet" />
      ) : (
        <div className="overflow-hidden rounded-lg border border-gray-200">
          <table className="min-w-full divide-y divide-gray-200 text-sm">
            <thead className="bg-gray-50">
              <tr>
                {['User', 'Role', 'Last login', 'Actions'].map((h) => (
                  <th key={h} className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">{h}</th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 bg-white">
              {users.map((u) => {
                const isSelf = u.id === currentUser?.id
                return (
                  <tr key={u.id}>
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-3">
                        <div className="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full bg-brand-100 text-xs font-bold text-brand-700">
                          {(u.name ?? u.email).slice(0, 2).toUpperCase()}
                        </div>
                        <div>
                          <p className="font-medium text-gray-900">{u.name ?? u.email}</p>
                          {u.name && <p className="text-xs text-gray-400">{u.email}</p>}
                        </div>
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      {isSelf ? (
                        <RoleBadge role={u.role} />
                      ) : (
                        <select
                          value={u.role}
                          onChange={(e) => changeRole.mutate({ userId: u.id, role: e.target.value as UserRole })}
                          className="rounded border border-gray-300 px-2 py-1 text-xs focus:outline-none focus:ring-1 focus:ring-brand-500"
                        >
                          {(['admin', 'operator', 'approver', 'viewer'] as UserRole[]).map((r) => (
                            <option key={r} value={r}>{r}</option>
                          ))}
                        </select>
                      )}
                    </td>
                    <td className="px-4 py-3 text-gray-400 text-xs">
                      {u.last_login ? new Date(u.last_login).toLocaleDateString() : 'Never'}
                    </td>
                    <td className="px-4 py-3">
                      {!isSelf && (
                        <button
                          onClick={() => setRevokeTarget(u)}
                          className="text-xs text-red-600 hover:underline"
                        >
                          Revoke
                        </button>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Invite modal */}
      {showInvite && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
          <div className="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
            <h2 className="text-base font-semibold text-gray-900 mb-4">Invite user</h2>
            {inviteLink ? (
              <div className="space-y-3">
                <p className="text-sm text-gray-600">Share this invite link with the user:</p>
                <div className="flex items-center gap-2 rounded-md border border-gray-300 bg-gray-50 px-3 py-2">
                  <code className="flex-1 text-xs break-all text-gray-800">{inviteLink}</code>
                  <CopyButton value={inviteLink} />
                </div>
                <button
                  onClick={() => { setShowInvite(false); setInviteLink(null) }}
                  className="rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700"
                >
                  Done
                </button>
              </div>
            ) : (
              <form onSubmit={handleSubmit(onInvite)} className="space-y-4">
                <div>
                  <Label>Email address</Label>
                  <Input type="email" {...register('email')} placeholder="user@example.com" />
                  <FieldError message={errors.email?.message} />
                </div>
                <div>
                  <Label>Role</Label>
                  <Select {...register('role')}>
                    <option value="viewer">Viewer — read only</option>
                    <option value="approver">Approver — decide approvals</option>
                    <option value="operator">Operator — gateway writes + reads</option>
                    <option value="admin">Admin — full access</option>
                  </Select>
                </div>
                <div className="flex justify-end gap-3">
                  <button type="button" onClick={() => setShowInvite(false)}
                    className="rounded border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50">
                    Cancel
                  </button>
                  <SaveButton isLoading={invite.isPending} label="Send invite" />
                </div>
              </form>
            )}
          </div>
        </div>
      )}

      {/* Revoke confirm */}
      {revokeTarget && (
        <ConfirmDialog
          title={`Revoke ${revokeTarget.name ?? revokeTarget.email}?`}
          description="This will immediately remove their access. This action cannot be undone."
          confirmLabel="Revoke access"
          destructive
          onConfirm={() => { revoke.mutate(revokeTarget.id); setRevokeTarget(null) }}
          onCancel={() => setRevokeTarget(null)}
        />
      )}
    </div>
  )
}

// ── Tab 3: Notifications ───────────────────────────────────────────────────────

const slackSchema = z.object({
  slack_webhook_url: z.string().url('Must be a valid URL').or(z.literal('')),
})
type SlackFormValues = z.infer<typeof slackSchema>

function NotificationsTab() {
  const { data: settings, isLoading } = useNotificationSettings()
  const update = useUpdateNotificationSettings()
  const test = useTestNotification()
  const [toast, setToast] = useState<{ msg: string; type: 'success' | 'error' } | null>(null)
  const [showWebhook, setShowWebhook] = useState(false)

  const { register, handleSubmit, formState: { errors } } = useForm<SlackFormValues>({
    resolver: zodResolver(slackSchema),
    defaultValues: { slack_webhook_url: '' },
  })

  async function onSaveSlack(values: SlackFormValues) {
    try {
      await update.mutateAsync({ slack_webhook_url: values.slack_webhook_url })
      setToast({ msg: 'Slack webhook saved.', type: 'success' })
    } catch {
      setToast({ msg: 'Failed to save.', type: 'error' })
    }
    setTimeout(() => setToast(null), 3000)
  }

  async function onTest() {
    try {
      const result = await test.mutateAsync('slack')
      setToast({ msg: result?.error ?? 'Test sent.', type: result?.success ? 'success' : 'error' })
    } catch {
      setToast({ msg: 'Test failed.', type: 'error' })
    }
    setTimeout(() => setToast(null), 4000)
  }

  if (isLoading) return <div className="flex justify-center py-16"><LoadingSpinner /></div>

  return (
    <div className="max-w-lg space-y-8">
      {/* Slack */}
      <section>
        <h3 className="text-sm font-semibold text-gray-900 mb-1">Slack</h3>
        <p className="text-xs text-gray-500 mb-4">Receive approval requests and alerts in a Slack channel.</p>

        {settings?.slack_enabled && !showWebhook ? (
          <div className="flex items-center gap-3 rounded-md border border-gray-200 bg-gray-50 px-4 py-3 text-sm">
            <span className="text-green-700 font-medium">✓ Webhook configured</span>
            <button onClick={() => setShowWebhook(true)} className="text-brand-600 hover:underline text-xs">Update</button>
          </div>
        ) : (
          <form onSubmit={handleSubmit(onSaveSlack)} className="space-y-3">
            <div>
              <Label>Webhook URL</Label>
              <div className="relative">
                <Input
                  type={showWebhook ? 'text' : 'password'}
                  placeholder="https://hooks.slack.com/services/…"
                  {...register('slack_webhook_url')}
                />
                <button
                  type="button"
                  onClick={() => setShowWebhook((v) => !v)}
                  className="absolute right-2 top-2 text-gray-400 hover:text-gray-600"
                >
                  {showWebhook ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </button>
              </div>
              <FieldError message={errors.slack_webhook_url?.message} />
            </div>
            <div className="flex items-center gap-3">
              <SaveButton isLoading={update.isPending} />
              {settings?.slack_enabled && (
                <button
                  type="button"
                  onClick={onTest}
                  disabled={test.isPending}
                  className="inline-flex items-center gap-1.5 rounded-md border border-gray-300 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-60"
                >
                  {test.isPending && <RefreshCw className="h-3.5 w-3.5 animate-spin" />}
                  Send test
                </button>
              )}
            </div>
          </form>
        )}

        {settings?.slack_enabled && showWebhook === false && (
          <button
            onClick={onTest}
            disabled={test.isPending}
            className="mt-2 inline-flex items-center gap-1.5 rounded-md border border-gray-300 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-60"
          >
            {test.isPending && <RefreshCw className="h-3.5 w-3.5 animate-spin" />}
            Send test message
          </button>
        )}

        {toast && <div className="mt-3"><Toast message={toast.msg} type={toast.type} /></div>}
      </section>

      {/* Email — coming soon */}
      <section>
        <h3 className="text-sm font-semibold text-gray-900 mb-1">Email (SMTP)</h3>
        <p className="text-xs text-gray-500 mb-4">Email dispatch is not yet enabled in this build.</p>
        <div className="space-y-3 opacity-50 pointer-events-none">
          <div>
            <Label>SMTP host</Label>
            <Input disabled placeholder="smtp.example.com" />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label>Port</Label>
              <Input type="number" disabled placeholder="587" />
            </div>
            <div>
              <Label>From address</Label>
              <Input type="email" disabled placeholder="noreply@example.com" />
            </div>
          </div>
          <p className="text-xs text-amber-600 font-medium">Coming soon — email dispatch is stubbed.</p>
        </div>
      </section>
    </div>
  )
}

// ── Tab 4: API Keys ────────────────────────────────────────────────────────────

const apiKeySchema = z.object({
  name: z.string().min(1, 'Key name is required'),
  expires_at: z.string().optional(),
})
type ApiKeyFormValues = z.infer<typeof apiKeySchema>

function ApiKeysTab() {
  const { data, isLoading } = useApiKeys()
  const createKey = useCreateApiKey()
  const revokeKey = useRevokeApiKey()

  const [showCreate, setShowCreate] = useState(false)
  const [newKey, setNewKey] = useState<string | null>(null)
  const [revokeTarget, setRevokeTarget] = useState<ApiKey | null>(null)
  const [showKey, setShowKey] = useState(false)

  const { register, handleSubmit, reset, formState: { errors } } = useForm<ApiKeyFormValues>({
    resolver: zodResolver(apiKeySchema),
    defaultValues: { name: '', expires_at: '' },
  })

  async function onCreate(values: ApiKeyFormValues) {
    const result = await createKey.mutateAsync({
      name: values.name,
      ...(values.expires_at ? { expires_at: values.expires_at } : {}),
    })
    if (result?.key) setNewKey(result.key)
    reset()
  }

  const keys: ApiKey[] = data?.data ?? []

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <p className="text-sm text-gray-500">{keys.length} key{keys.length !== 1 ? 's' : ''}</p>
        <button
          onClick={() => { setShowCreate(true); setNewKey(null) }}
          className="rounded-md bg-brand-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-brand-700"
        >
          Create API key
        </button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-8"><LoadingSpinner /></div>
      ) : keys.length === 0 ? (
        <EmptyState title="No API keys" description="Create a key to authenticate the collector or other services." />
      ) : (
        <div className="overflow-hidden rounded-lg border border-gray-200">
          <table className="min-w-full divide-y divide-gray-200 text-sm">
            <thead className="bg-gray-50">
              <tr>
                {['Name', 'Prefix', 'Created', 'Last used', ''].map((h, i) => (
                  <th key={i} className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">{h}</th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 bg-white">
              {keys.map((k) => (
                <tr key={k.id}>
                  <td className="px-4 py-3 font-medium text-gray-900">{k.name}</td>
                  <td className="px-4 py-3 font-mono text-xs text-gray-500">{k.prefix.slice(0, 7)}…</td>
                  <td className="px-4 py-3 text-gray-400 text-xs">{new Date(k.created_at).toLocaleDateString()}</td>
                  <td className="px-4 py-3 text-gray-400 text-xs">
                    {k.last_used ? new Date(k.last_used).toLocaleDateString() : '—'}
                  </td>
                  <td className="px-4 py-3">
                    <button
                      onClick={() => setRevokeTarget(k)}
                      className="text-xs text-red-600 hover:underline"
                    >
                      Revoke
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Create modal */}
      {showCreate && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
          <div className="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
            <h2 className="text-base font-semibold text-gray-900 mb-4">Create API key</h2>
            {newKey ? (
              <div className="space-y-4">
                <div className="rounded-md border border-amber-300 bg-amber-50 px-4 py-3">
                  <p className="text-sm font-semibold text-amber-800 mb-2">⚠ This key will not be shown again</p>
                  <div className="flex items-center gap-2 rounded bg-white border border-amber-200 px-3 py-2">
                    <code className="flex-1 text-xs break-all font-mono text-gray-900">
                      {showKey ? newKey : newKey.replace(/./g, '•')}
                    </code>
                    <button onClick={() => setShowKey((v) => !v)} className="text-gray-400 hover:text-gray-700">
                      {showKey ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                    </button>
                    <CopyButton value={newKey} />
                  </div>
                </div>
                <button
                  onClick={() => { setShowCreate(false); setNewKey(null); setShowKey(false) }}
                  className="w-full rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700"
                >
                  I've copied the key — Done
                </button>
              </div>
            ) : (
              <form onSubmit={handleSubmit(onCreate)} className="space-y-4">
                <div>
                  <Label>Key name</Label>
                  <Input {...register('name')} placeholder="e.g. Production collector" />
                  <FieldError message={errors.name?.message} />
                </div>
                <div>
                  <Label>Expiry date (optional)</Label>
                  <Input type="date" {...register('expires_at')} min={new Date().toISOString().split('T')[0]} />
                </div>
                <div className="flex justify-end gap-3">
                  <button type="button" onClick={() => setShowCreate(false)}
                    className="rounded border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50">
                    Cancel
                  </button>
                  <SaveButton isLoading={createKey.isPending} label="Create key" />
                </div>
              </form>
            )}
          </div>
        </div>
      )}

      {/* Revoke confirm */}
      {revokeTarget && (
        <ConfirmDialog
          title={`Revoke "${revokeTarget.name}"?`}
          description="Any services using this key will lose access immediately."
          confirmLabel="Revoke key"
          destructive
          onConfirm={() => { revokeKey.mutate(revokeTarget.id); setRevokeTarget(null) }}
          onCancel={() => setRevokeTarget(null)}
        />
      )}
    </div>
  )
}

// ── Tab bar + routing ──────────────────────────────────────────────────────────

type TabId = 'org' | 'users' | 'notifications' | 'api-keys'
const TABS: { id: TabId; label: string }[] = [
  { id: 'org', label: 'Organisation' },
  { id: 'users', label: 'Users' },
  { id: 'notifications', label: 'Notifications' },
  { id: 'api-keys', label: 'API Keys' },
]

// ── Main page ──────────────────────────────────────────────────────────────────

export function SettingsPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const tabParam = searchParams.get('tab') as TabId | null
  const activeTab: TabId = TABS.some((t) => t.id === tabParam) ? (tabParam as TabId) : 'org'

  function setTab(id: TabId) {
    setSearchParams({ tab: id }, { replace: true })
  }

  // Scroll to top on tab change
  const contentRef = useRef<HTMLDivElement>(null)
  useEffect(() => { contentRef.current?.scrollTo(0, 0) }, [activeTab])

  return (
    <div className="flex h-full flex-col">
      <Topbar title="Settings" />

      {/* Tab bar */}
      <div className="border-b border-gray-200 bg-white px-6">
        <nav className="-mb-px flex gap-6">
          {TABS.map((tab) => (
            <button
              key={tab.id}
              onClick={() => setTab(tab.id)}
              className={`py-3 text-sm font-medium border-b-2 transition-colors ${
                activeTab === tab.id
                  ? 'border-brand-600 text-brand-700'
                  : 'border-transparent text-gray-500 hover:text-gray-700'
              }`}
            >
              {tab.label}
            </button>
          ))}
        </nav>
      </div>

      {/* Tab content */}
      <div ref={contentRef} className="flex-1 overflow-y-auto p-6">
        {activeTab === 'org' && <OrgTab />}
        {activeTab === 'users' && <UsersTab />}
        {activeTab === 'notifications' && <NotificationsTab />}
        {activeTab === 'api-keys' && <ApiKeysTab />}
      </div>
    </div>
  )
}
