// PolicyPanel.tsx -- shared create/edit policy form, extracted from
// PoliciesPage.tsx (B-210) so the new workspace-scoped policy view can
// reuse the exact same form/field set rather than reinventing it. The
// backend's policy model (workspace-scoped or org-wide) is identical --
// same conditions, same action/status/priority shape -- so the real
// difference between "admin editing the org floor" and "workspace_admin
// editing their own workspace's policy" is only WHICH mutation the submit
// calls, not the form itself. Generalized to accept the create/update
// mutations as props (same minimal shape react-query's useMutation
// already returns) instead of calling useCreatePolicy/useUpdatePolicy
// directly, so each caller wires its own real endpoint.
import { useState } from 'react'
import { SlideOverPanel, Button } from '@/components/common'
import { useToast } from '@/components/common/Toast'
import type { Policy, PolicyCreate, PolicyUpdate } from '@/hooks/usePolicies'

export type PanelMode = 'create' | 'edit'

interface MutationLike<TBody> {
  mutateAsync: (body: TBody) => Promise<unknown>
  isPending: boolean
}

interface PolicyPanelProps {
  mode: PanelMode
  policy?: Policy
  onClose: () => void
  createMutation: MutationLike<PolicyCreate>
  updateMutation: MutationLike<{ id: string; body: PolicyUpdate }>
}

const ENVIRONMENTS = ['any', 'production', 'staging', 'development'] as const

export function PolicyPanel({ mode, policy, onClose, createMutation, updateMutation }: PolicyPanelProps) {
  const { showToast } = useToast()

  const [name, setName] = useState(policy?.name ?? '')
  const [description, setDescription] = useState(policy?.description ?? '')
  const [priority, setPriority] = useState(policy?.priority ?? 10)
  const [action, setAction] = useState<'allow' | 'deny' | 'escalate'>(
    (policy?.action as 'allow' | 'deny' | 'escalate') ?? 'deny'
  )
  const [status, setStatus] = useState<'active' | 'draft' | 'disabled'>(
    (policy?.status as 'active' | 'draft' | 'disabled') ?? 'draft'
  )
  const [alert, setAlert] = useState(policy?.alert ?? false)

  const [agentPattern, setAgentPattern] = useState(policy?.conditions.agent_name_pattern ?? '')
  const [toolNames, setToolNames]       = useState(policy?.conditions.tool_names?.join(', ') ?? '')
  const [actionTypes, setActionTypes]   = useState(policy?.conditions.action_types?.join(', ') ?? '')
  const [environments, setEnvironments] = useState<string[]>(policy?.conditions.environments ?? [])
  const [recordCountGt, setRecordCountGt] = useState(policy?.conditions.record_count_gt?.toString() ?? '')
  const [semanticRule, setSemanticRule] = useState(policy?.conditions.semantic_rule ?? '')
  const [scopeDrift, setScopeDrift]     = useState(policy?.conditions.scope_drift ?? false)

  const isPending = createMutation.isPending || updateMutation.isPending

  function buildConditions() {
    return {
      agent_name_pattern: agentPattern.trim() || undefined,
      tool_names: toolNames.trim() ? toolNames.split(',').map(s => s.trim()).filter(Boolean) : undefined,
      action_types: actionTypes.trim() ? actionTypes.split(',').map(s => s.trim()).filter(Boolean) : undefined,
      environments: environments.length ? (environments as ('production' | 'staging' | 'development' | 'any')[]) : undefined,
      record_count_gt: recordCountGt ? parseInt(recordCountGt, 10) : undefined,
      semantic_rule: semanticRule.trim() || undefined,
      scope_drift: scopeDrift || undefined,
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    try {
      if (mode === 'create') {
        const body: PolicyCreate = {
          name, description: description || undefined, priority, action, alert,
          status: status === 'disabled' ? 'draft' : status,
          conditions: buildConditions(),
        }
        await createMutation.mutateAsync(body)
      } else {
        const body: PolicyUpdate = {
          name, description: description || undefined, priority, action, alert, status,
          conditions: buildConditions(),
        }
        await updateMutation.mutateAsync({ id: policy!.id, body })
      }
      showToast(mode === 'create' ? 'Policy created' : 'Policy saved', { type: 'success' })
      onClose()
    } catch {
      showToast('Save failed', { type: 'error' })
    }
  }

  function toggleEnv(env: string) {
    setEnvironments(prev => prev.includes(env) ? prev.filter(e => e !== env) : [...prev, env])
  }

  return (
    <SlideOverPanel onClose={onClose}>
      <div className="flex items-center justify-between px-6 py-4 border-b">
        <div>
          <h2 className="font-semibold text-gray-900">
            {mode === 'create' ? 'New Policy' : 'Edit Policy'}
          </h2>
          {policy && <p className="text-xs text-gray-500 truncate">{policy.name}</p>}
        </div>
        <button onClick={onClose} className="text-gray-400 hover:text-gray-600 text-xl leading-none">x</button>
      </div>

      <div className="flex-1 overflow-y-auto px-6 py-4">
        <form id="policy-form" onSubmit={handleSubmit} className="space-y-6">

          <div className="space-y-4">
            <p className="text-xs font-semibold text-gray-500 uppercase tracking-wide">Basic</p>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Name</label>
              <input required value={name} onChange={e => setName(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="Block production deletes" />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Description</label>
              <textarea value={description} onChange={e => setDescription(e.target.value)} rows={2}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Priority (lower = first)</label>
                <input type="number" min={1} required value={priority}
                  onChange={e => setPriority(parseInt(e.target.value, 10) || 1)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Status</label>
                <select value={status} onChange={e => setStatus(e.target.value as typeof status)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  <option value="active">Active</option>
                  <option value="draft">Draft</option>
                  {mode === 'edit' && <option value="disabled">Disabled</option>}
                </select>
              </div>
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Action</label>
                <select value={action} onChange={e => setAction(e.target.value as typeof action)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  <option value="allow">Allow</option>
                  <option value="deny">Deny</option>
                  <option value="escalate">Escalate (require approval)</option>
                </select>
              </div>
              <div className="flex items-end pb-2">
                <label className="flex items-center gap-2 text-sm text-gray-700 cursor-pointer">
                  <input type="checkbox" checked={alert} onChange={e => setAlert(e.target.checked)}
                    className="rounded border-gray-300 text-indigo-600 focus:ring-indigo-500" />
                  Send alert on match
                </label>
              </div>
            </div>
          </div>

          <div className="space-y-4 pt-4 border-t">
            <p className="text-xs font-semibold text-gray-500 uppercase tracking-wide">Conditions (all specified must match)</p>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Agent name pattern (glob)</label>
              <input value={agentPattern} onChange={e => setAgentPattern(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="claude-support-* or leave blank for any" />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Tool names (comma-separated)</label>
              <input value={toolNames} onChange={e => setToolNames(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="delete_file, drop_table" />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Action verbs (comma-separated)</label>
              <input value={actionTypes} onChange={e => setActionTypes(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="delete, drop, truncate" />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">Environments</label>
              <div className="flex flex-wrap gap-3">
                {ENVIRONMENTS.map(env => (
                  <label key={env} className="flex items-center gap-1.5 text-sm text-gray-700 cursor-pointer">
                    <input type="checkbox" checked={environments.includes(env)} onChange={() => toggleEnv(env)}
                      className="rounded border-gray-300 text-indigo-600 focus:ring-indigo-500" />
                    <span className="font-mono text-xs">{env}</span>
                  </label>
                ))}
              </div>
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Estimated records greater than</label>
              <input type="number" min={0} value={recordCountGt} onChange={e => setRecordCountGt(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="e.g. 1000 -- leave blank to skip" />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Semantic rule (LLM-evaluated)</label>
              <textarea value={semanticRule} onChange={e => setSemanticRule(e.target.value)} rows={2}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="Agent must not exfiltrate PII" />
            </div>

            <label className="flex items-center gap-2 text-sm text-gray-700 cursor-pointer">
              <input type="checkbox" checked={scopeDrift} onChange={e => setScopeDrift(e.target.checked)}
                className="rounded border-gray-300 text-indigo-600 focus:ring-indigo-500" />
              Match on scope drift (agent acts outside declared task)
            </label>
          </div>
        </form>
      </div>

      <div className="px-6 py-4 border-t flex gap-3">
        <Button type="submit" form="policy-form" isLoading={isPending} className="flex-1">
          {mode === 'create' ? 'Create policy' : 'Save changes'}
        </Button>
        <Button variant="secondary" onClick={onClose} disabled={isPending}>Cancel</Button>
      </div>
    </SlideOverPanel>
  )
}
