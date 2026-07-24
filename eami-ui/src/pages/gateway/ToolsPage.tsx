// ToolsPage.tsx -- Gateway / Tools
// Owned by FE-Gateway
import { useState } from 'react'
import { Plus, Trash2, Zap, CheckCircle, AlertCircle, WifiOff, HelpCircle } from 'lucide-react'
import {
  PageHeader,
  ConfirmDialog,
  EmptyState,
  LoadingSpinner,
} from '@/components/common'
import {
  useTools,
  useCreateTool,
  useDeleteTool,
  useTestTool,
} from '@/hooks/useTools'
import type { Tool, ToolCreate } from '@/hooks/useTools'

// Status badge

const STATUS_CONFIG = {
  connected:    { icon: CheckCircle, bg: 'bg-green-100 text-green-800' },
  degraded:     { icon: AlertCircle, bg: 'bg-amber-100 text-amber-800' },
  disconnected: { icon: WifiOff,     bg: 'bg-red-100 text-red-800' },
}

function StatusBadge({ status }: { status: string }) {
  const cfg = STATUS_CONFIG[status as keyof typeof STATUS_CONFIG]
  if (!cfg) return <span className="text-xs text-gray-400">{status}</span>
  const Icon = cfg.icon
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium capitalize ${cfg.bg}`}>
      <Icon className="h-3 w-3" />
      {status}
    </span>
  )
}

// Test-connection result state -- one entry per outcome the backend
// (eami-api's TestTool, B-023) can actually report, so a rejected
// credential looks nothing like a genuinely reachable tool. Reuses
// STATUS_CONFIG's color language (green/amber/red) plus a neutral gray
// for "we couldn't even attempt this" (misconfigured/mcp).
type TestState = 'testing' | 'success' | 'auth-failed' | 'unreachable' | 'misconfigured' | 'failed'

const TEST_STATE_CONFIG: Record<Exclude<TestState, 'testing'>, { label: string; icon: typeof CheckCircle; className: string }> = {
  success:       { label: 'OK',            icon: CheckCircle, className: 'text-green-700 bg-green-50' },
  'auth-failed': { label: 'Auth failed',   icon: AlertCircle, className: 'text-amber-700 bg-amber-50' },
  unreachable:   { label: 'Unreachable',   icon: WifiOff,     className: 'text-red-700 bg-red-50' },
  misconfigured: { label: 'Misconfigured', icon: HelpCircle,  className: 'text-gray-600 bg-gray-100' },
  failed:        { label: 'Failed',        icon: AlertCircle, className: 'text-red-700 bg-red-50' },
}

// Parses B-023's "<reason>: <detail>" error string (tool_connectivity.go's
// errorMessage()) into one of the known reasons, falling back to a generic
// "failed" bucket for anything unrecognized rather than silently mislabeling
// it as a specific reason it didn't actually report.
function classifyTestError(message: string | null | undefined): Exclude<TestState, 'testing' | 'success'> {
  const reason = message?.split(':')[0]?.trim()
  if (reason === 'auth-failed' || reason === 'unreachable' || reason === 'misconfigured') return reason
  return 'failed'
}

function TypeBadge({ type }: { type: string }) {
  const map: Record<string, string> = {
    mcp:      'bg-purple-100 text-purple-800',
    rest_api: 'bg-blue-100 text-blue-800',
    database: 'bg-orange-100 text-orange-800',
  }
  return (
    <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${map[type] ?? 'bg-gray-100 text-gray-600'}`}>
      {type.replace('_', ' ')}
    </span>
  )
}

// Add Tool panel

function AddToolPanel({ onClose }: { onClose: () => void }) {
  const create = useCreateTool()
  const [toast, setToast] = useState<string | null>(null)

  const [name, setName]         = useState('')
  const [type, setType]         = useState<'mcp' | 'rest_api' | 'database'>('mcp')
  const [authType, setAuthType] = useState<'oauth2' | 'api_key' | 'basic' | 'db_connection_string'>('api_key')
  const [mcpCommand, setMcpCommand] = useState('')
  const [mcpArgs, setMcpArgs]   = useState('')
  const [baseUrl, setBaseUrl]   = useState('')
  const [credKey, setCredKey]   = useState('')
  const [connStr, setConnStr]   = useState('')

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    try {
      const body: ToolCreate = {
        name, type, auth_type: authType,
        mcp_command: type === 'mcp' ? mcpCommand || undefined : undefined,
        mcp_args: type === 'mcp' && mcpArgs ? mcpArgs.split(' ').filter(Boolean) : undefined,
        base_url: type === 'rest_api' ? baseUrl || undefined : undefined,
        credentials: {
          api_key: authType === 'api_key' ? credKey || undefined : undefined,
          connection_string: authType === 'db_connection_string' ? connStr || undefined : undefined,
        },
      }
      await create.mutateAsync(body)
      setToast('Tool added')
      setTimeout(() => { setToast(null); onClose() }, 1000)
    } catch {
      setToast('Failed to add tool')
    }
  }

  return (
    <div className="fixed inset-y-0 right-0 w-[440px] bg-white shadow-xl flex flex-col z-50 border-l border-gray-200">
      <div className="flex items-center justify-between px-6 py-4 border-b">
        <h2 className="font-semibold text-gray-900">Add Tool Connection</h2>
        <button onClick={onClose} className="text-gray-400 hover:text-gray-600 text-xl leading-none">x</button>
      </div>

      {toast && (
        <div className={`mx-6 mt-4 px-4 py-2 rounded text-sm border ${
          toast.includes('Failed') ? 'bg-red-50 border-red-200 text-red-700' : 'bg-green-50 border-green-200 text-green-700'
        }`}>
          {toast}
        </div>
      )}

      <div className="flex-1 overflow-y-auto px-6 py-4">
        <form id="tool-form" onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Tool name</label>
            <input required value={name} onChange={e => setName(e.target.value)}
              className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
              placeholder="Salesforce, Postgres prod..." />
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Type</label>
              <select value={type} onChange={e => setType(e.target.value as typeof type)}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                <option value="mcp">MCP</option>
                <option value="rest_api">REST API</option>
                <option value="database">Database</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Auth type</label>
              <select value={authType} onChange={e => setAuthType(e.target.value as typeof authType)}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                <option value="api_key">API key</option>
                <option value="oauth2">OAuth 2</option>
                <option value="basic">Basic auth</option>
                <option value="db_connection_string">Connection string</option>
              </select>
            </div>
          </div>

          {type === 'mcp' && (
            <>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">MCP command</label>
                <input value={mcpCommand} onChange={e => setMcpCommand(e.target.value)}
                  className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  placeholder="npx -y @modelcontextprotocol/server-..." />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">MCP args (space-separated)</label>
                <input value={mcpArgs} onChange={e => setMcpArgs(e.target.value)}
                  className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  placeholder="--port 3001" />
              </div>
            </>
          )}

          {type === 'rest_api' && (
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Base URL</label>
              <input value={baseUrl} onChange={e => setBaseUrl(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="https://api.example.com/v1" />
            </div>
          )}

          {authType === 'api_key' && (
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">API key (stored encrypted)</label>
              <input type="password" value={credKey} onChange={e => setCredKey(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="sk-..." />
            </div>
          )}

          {authType === 'db_connection_string' && (
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Connection string (stored encrypted)</label>
              <input type="password" value={connStr} onChange={e => setConnStr(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="postgresql://user:pass@host/db" />
            </div>
          )}
        </form>
      </div>

      <div className="px-6 py-4 border-t flex gap-3">
        <button type="submit" form="tool-form" disabled={create.isPending}
          className="flex-1 bg-indigo-600 text-white rounded px-4 py-2 text-sm font-medium hover:bg-indigo-700 disabled:opacity-50">
          {create.isPending ? 'Adding...' : 'Add tool'}
        </button>
        <button onClick={onClose} className="px-4 py-2 text-sm text-gray-600 hover:text-gray-900">Cancel</button>
      </div>
    </div>
  )
}

// Helpers

function formatLastUsed(iso?: string | null): string {
  if (!iso) return '--'
  const diff = Date.now() - new Date(iso).getTime()
  const mins = Math.floor(diff / 60_000)
  if (mins < 1) return 'just now'
  if (mins < 60) return mins + 'm ago'
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return hrs + 'h ago'
  return Math.floor(hrs / 24) + 'd ago'
}

// Main page

export function ToolsPage() {
  const { data, isLoading, error } = useTools()
  const deleteTool = useDeleteTool()
  const testTool   = useTestTool()

  const [showAdd, setShowAdd]           = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<Tool | null>(null)
  const [testResult, setTestResult]     = useState<Record<string, { state: TestState; message?: string }>>({})

  const tools: Tool[] = (data as any)?.data ?? []

  async function handleTest(id: string) {
    setTestResult(prev => ({ ...prev, [id]: { state: 'testing' } }))
    try {
      // TestTool (B-023) always resolves with a 200 -- the real result
      // lives in the body's success/error fields, not in whether this
      // promise rejects. Only a genuine transport/HTTP-level failure
      // (network error, tool not found, etc.) throws.
      const result = await testTool.mutateAsync(id)
      if (result?.success) {
        setTestResult(prev => ({ ...prev, [id]: { state: 'success' } }))
      } else {
        const message = result?.error ?? undefined
        setTestResult(prev => ({ ...prev, [id]: { state: classifyTestError(message), message } }))
      }
    } catch {
      setTestResult(prev => ({ ...prev, [id]: { state: 'failed', message: 'Request failed' } }))
    }
    // Longer than before (4s -> 6s) since there's now a specific reason
    // worth reading, not just a flash of green/red.
    setTimeout(() => setTestResult(prev => { const n = { ...prev }; delete n[id]; return n }), 6000)
  }

  if (isLoading) return <div className="p-6"><LoadingSpinner /></div>
  if (error)    return <div className="p-6 text-sm text-red-500">Failed to load tools.</div>

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title="Tools"
        subtitle="MCP servers and API connections the gateway can route calls to"
        actions={
          <button onClick={() => setShowAdd(true)}
            className="flex items-center gap-1.5 bg-indigo-600 text-white rounded px-3 py-1.5 text-sm font-medium hover:bg-indigo-700">
            <Plus className="h-4 w-4" />
            Add tool
          </button>
        }
      />

      <div className="flex-1 overflow-auto p-6">
        {tools.length === 0 ? (
          <EmptyState
            title="No tools connected"
            description="Add an MCP server or REST API to allow gateway-controlled access."
          />
        ) : (
          <div className="rounded-lg border border-gray-200 overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-xs text-gray-500 uppercase">
                <tr>
                  <th className="px-4 py-3 text-left">Name</th>
                  <th className="px-4 py-3 text-left">Type</th>
                  <th className="px-4 py-3 text-left">Auth</th>
                  <th className="px-4 py-3 text-left">Status</th>
                  <th className="px-4 py-3 text-left">Endpoint / Command</th>
                  <th className="px-4 py-3 text-left">Last used</th>
                  <th className="px-4 py-3 text-right"></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {tools.map(tool => {
                  const tr = testResult[tool.id]
                  return (
                    <tr key={tool.id} className="hover:bg-gray-50">
                      <td className="px-4 py-3 font-medium text-gray-900">{tool.name}</td>
                      <td className="px-4 py-3"><TypeBadge type={tool.type} /></td>
                      <td className="px-4 py-3 text-xs text-gray-500 font-mono">{tool.auth_type ?? '--'}</td>
                      <td className="px-4 py-3"><StatusBadge status={tool.status} /></td>
                      <td className="px-4 py-3 text-xs font-mono text-gray-500 max-w-xs truncate">
                        {tool.mcp_command ?? tool.base_url ?? '--'}
                      </td>
                      <td className="px-4 py-3 text-xs text-gray-400">{formatLastUsed(tool.last_used)}</td>
                      <td className="px-4 py-3 text-right">
                        <div className="flex items-center justify-end gap-3">
                          {(() => {
                            const cfg = tr && tr.state !== 'testing' ? TEST_STATE_CONFIG[tr.state] : undefined
                            const Icon = cfg?.icon ?? Zap
                            return (
                              <button
                                onClick={() => handleTest(tool.id)}
                                disabled={tr?.state === 'testing'}
                                title={tr?.message ?? 'Test connection'}
                                className={`flex items-center gap-1 text-xs font-medium px-2 py-1 rounded ${
                                  cfg ? cfg.className :
                                  tr?.state === 'testing' ? 'text-gray-400' :
                                  'text-indigo-600 hover:text-indigo-800'
                                }`}
                              >
                                <Icon className="h-3 w-3" />
                                {tr?.state === 'testing' ? 'Testing...' : cfg ? cfg.label : 'Test'}
                              </button>
                            )
                          })()}
                          <button onClick={() => setDeleteTarget(tool)}
                            className="text-gray-400 hover:text-red-600" title="Remove">
                            <Trash2 className="h-4 w-4" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {showAdd && (
        <>
          <div className="fixed inset-0 bg-black/20 z-40" onClick={() => setShowAdd(false)} />
          <AddToolPanel onClose={() => setShowAdd(false)} />
        </>
      )}

      {deleteTarget && (
        <ConfirmDialog
          open
          title={'Remove "' + deleteTarget.name + '"?'}
          description="The tool connection will be removed. Agents referencing this tool will fail until reconnected."
          confirmLabel="Remove"
          destructive
          isLoading={deleteTool.isPending}
          onConfirm={() => { deleteTool.mutate(deleteTarget.id, { onSuccess: () => setDeleteTarget(null) }) }}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  )
}
