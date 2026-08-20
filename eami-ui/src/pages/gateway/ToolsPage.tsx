// ToolsPage.tsx -- Gateway / Tools
// Owned by FE-Gateway
import { useState } from 'react'
import { Plus, Trash2, Pencil, Zap, CheckCircle, AlertCircle, WifiOff, HelpCircle } from 'lucide-react'
import {
  PageHeader,
  ConfirmDialog,
  EmptyState,
  LoadingSpinner,
} from '@/components/common'
import {
  useTools,
  useCreateTool,
  useUpdateTool,
  useDeleteTool,
  useTestTool,
  useDiscoverOpenAPI,
} from '@/hooks/useTools'
import type { ActionPathMapping, OpenAPIDiscoverResult, ToolAuditMode, ToolCreateWithActions, ToolDataHandling, ToolProvider, ToolType, ToolUpdate, ToolWithActions } from '@/hooks/useTools'

// AI provider connectors (Thread A Model 1): the full set of providers a
// second connector could target. Only "claude" has a real gateway adapter
// today -- listed here as a real dropdown (not a hardcoded single value)
// specifically so a future provider is one more entry, not a form rework.
const AI_PROVIDERS: { value: ToolProvider; label: string }[] = [
  { value: 'claude', label: 'Claude (Anthropic)' },
]

const AUDIT_MODES: { value: ToolAuditMode; label: string; hint: string }[] = [
  { value: 'structural_metadata_only', label: 'Structural metadata only (recommended)', hint: 'Audit log records tool/action/decision/timing -- never the prompt content.' },
  { value: 'full', label: 'Full', hint: 'Audit log includes the full request, including prompt content. Use only if your compliance posture requires it.' },
]

// Data-handling visibility (B-078) -- a VISIBILITY designation only, not a
// technical control: EAMI cannot enforce what a third-party AI provider
// does with dispatched data, this records what the admin has confirmed
// the actual commercial agreement says. "unknown" is listed first and is
// the fail-safe default, deliberately not implying any particular
// retention posture until an admin explicitly confirms one.
const DATA_HANDLING_OPTIONS: { value: ToolDataHandling; label: string }[] = [
  { value: 'unknown', label: 'Unknown / not configured' },
  { value: 'zero_retention', label: 'Zero Data Retention enabled' },
  { value: 'standard_retention', label: 'Standard retention (no ZDR)' },
]

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
    mcp:          'bg-purple-100 text-purple-800',
    rest_api:     'bg-blue-100 text-blue-800',
    database:     'bg-orange-100 text-orange-800',
    ai_provider:  'bg-teal-100 text-teal-800',
  }
  return (
    <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${map[type] ?? 'bg-gray-100 text-gray-600'}`}>
      {type.replace('_', ' ')}
    </span>
  )
}

// Action mappings editor (B-046) -- rest_api-only. Lets an admin define
// per-action path/method routing instead of every action flatly POSTing
// to base_url (B-044's original behavior, still the fallback for any
// action left unmapped here).

// inputSchema (B-075) travels alongside a row only when it came from
// OpenAPI discovery -- a manually-typed row never sets it, exactly
// matching this type's pre-B-075 shape for that case. There is
// deliberately no separate "hasPathParams" flag on a row: a code-review
// finding on this brief caught that a static flag set once at add-time
// goes stale the moment an admin edits the path (either fixing a
// discovered `{param}` by hand, or typing a brand-new row that happens to
// contain one) -- ActionPathsEditor below derives the warning live from
// row.path on every render instead, so it's always correct.
type ActionPathRow = { action: string; path: string; method: string; inputSchema?: Record<string, unknown> }

const HTTP_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'] as const

// Converts editable rows to the {action: {path, method}} shape the API
// expects, silently dropping rows with no action name or no path -- an
// in-progress blank row shouldn't become a stored mapping to "" against
// the tool's base_url. Duplicate action names: last row wins, matching
// ordinary object-literal semantics rather than being flagged as an error.
function rowsToActionPaths(rows: ActionPathRow[]): Record<string, ActionPathMapping> {
  const out: Record<string, ActionPathMapping> = {}
  for (const row of rows) {
    const action = row.action.trim()
    const path = row.path.trim()
    if (!action || !path) continue
    out[action] = { path, method: row.method, input_schema: row.inputSchema }
  }
  return out
}

function ActionPathsEditor({ rows, onChange }: { rows: ActionPathRow[]; onChange: (rows: ActionPathRow[]) => void }) {
  function updateRow(i: number, patch: Partial<ActionPathRow>) {
    onChange(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  }
  function removeRow(i: number) {
    onChange(rows.filter((_, idx) => idx !== i))
  }
  function addRow() {
    onChange([...rows, { action: '', path: '', method: 'POST' }])
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-1">
        <label className="block text-sm font-medium text-gray-700">Action mappings</label>
        <button type="button" onClick={addRow} className="text-xs text-indigo-600 hover:text-indigo-800 font-medium">
          + Add mapping
        </button>
      </div>
      <p className="text-xs text-gray-400 mb-2">
        Route specific actions to their own endpoint under this tool's base URL. Any action left unmapped here falls back to a plain POST to the base URL.
      </p>
      {rows.length === 0 ? (
        <p className="text-xs text-gray-400 italic">No per-action mappings -- every action uses the base URL.</p>
      ) : (
        <div className="space-y-2">
          {rows.map((row, i) => (
            <div key={i}>
              <div className="flex items-center gap-2">
                <input value={row.action} onChange={e => updateRow(i, { action: e.target.value })}
                  placeholder="action name"
                  className="w-1/3 border rounded px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500" />
                <select value={row.method} onChange={e => updateRow(i, { method: e.target.value })}
                  className="border rounded px-1.5 py-1.5 text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  {HTTP_METHODS.map(m => <option key={m} value={m}>{m}</option>)}
                </select>
                <input value={row.path} onChange={e => updateRow(i, { path: e.target.value })}
                  placeholder="/contacts"
                  className="flex-1 border rounded px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500" />
                <button type="button" onClick={() => removeRow(i)} className="text-gray-400 hover:text-red-600" title="Remove mapping">
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </div>
              {row.path.includes('{') && (
                <p className="flex items-center gap-1 text-[11px] text-amber-700 mt-0.5 ml-0.5">
                  <AlertCircle className="h-3 w-3 shrink-0" />
                  Has a <code className="font-mono">{'{param}'}</code> in its path -- the gateway doesn't substitute path parameters yet, so this action won't reach the intended endpoint until you rewrite the path.
                </p>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// OpenAPI-spec action discovery (B-075) -- rest_api-only, lets an admin
// paste/upload an OpenAPI spec (or point at a URL) and turn its real
// operations into candidate action mappings, instead of hand-typing every
// path/method. Purely a preview: "Add selected" only ever writes into the
// SAME actionRows state ActionPathsEditor above already manages -- nothing
// reaches the backend until the panel's own pre-existing Save button is
// clicked, so nothing here can silently activate an unreviewed action.
function OpenAPIDiscoverySection({ onAddActions }: { onAddActions: (rows: ActionPathRow[]) => void }) {
  const discover = useDiscoverOpenAPI()
  const [mode, setMode] = useState<'url' | 'paste'>('url')
  const [specUrl, setSpecUrl] = useState('')
  const [specContent, setSpecContent] = useState('')
  const [result, setResult] = useState<OpenAPIDiscoverResult | null>(null)
  const [selected, setSelected] = useState<Record<string, boolean>>({})
  const [error, setError] = useState<string | null>(null)

  const canDiscover = mode === 'url' ? specUrl.trim().length > 0 : specContent.trim().length > 0

  async function handleDiscover() {
    setError(null)
    setResult(null)
    try {
      const body = mode === 'url' ? { spec_url: specUrl.trim() } : { spec_content: specContent }
      const res = await discover.mutateAsync(body)
      setResult(res)
      // Default a path-param action to UNselected -- it's real per the
      // spec, but the gateway can't dispatch it correctly yet (see the
      // warning shown per-action below), so the safer default is "admin
      // has to deliberately opt in," not "silently include something that
      // won't work."
      const initial: Record<string, boolean> = {}
      for (const [name, action] of Object.entries(res.actions)) {
        initial[name] = !action.has_path_params
      }
      setSelected(initial)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to parse spec')
    }
  }

  function handleAddSelected() {
    if (!result) return
    const rows: ActionPathRow[] = Object.entries(result.actions)
      .filter(([name]) => selected[name])
      .map(([name, a]) => ({
        action: name, path: a.path, method: a.method,
        inputSchema: a.input_schema,
      }))
    if (rows.length === 0) return
    onAddActions(rows)
    setResult(null)
    setSpecUrl('')
    setSpecContent('')
  }

  const actionEntries = result ? Object.entries(result.actions) : []
  const tabClass = (active: boolean) =>
    `px-2.5 py-1 text-xs font-medium rounded ${active ? 'bg-indigo-600 text-white' : 'bg-white text-gray-600 border'}`

  return (
    <div className="border rounded-lg p-3 bg-gray-50">
      <label className="block text-sm font-medium text-gray-700 mb-1">Discover actions from an OpenAPI spec</label>
      <p className="text-xs text-gray-400 mb-2">Populate action mappings automatically from a real OpenAPI 3.x spec, instead of typing each one by hand. Nothing is saved until you review the results below and click Save.</p>

      <div className="flex gap-1.5 mb-2">
        <button type="button" onClick={() => setMode('url')} className={tabClass(mode === 'url')}>URL</button>
        <button type="button" onClick={() => setMode('paste')} className={tabClass(mode === 'paste')}>Paste spec</button>
      </div>

      {mode === 'url' ? (
        <input value={specUrl} onChange={e => setSpecUrl(e.target.value)}
          placeholder="https://api.example.com/openapi.json"
          className="w-full border rounded px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500" />
      ) : (
        <textarea value={specContent} onChange={e => setSpecContent(e.target.value)} rows={4}
          placeholder="Paste an OpenAPI 3.x spec (JSON or YAML)"
          className="w-full border rounded px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500" />
      )}

      <button type="button" onClick={handleDiscover} disabled={!canDiscover || discover.isPending}
        className="mt-2 text-xs bg-white border text-indigo-700 font-medium rounded px-2.5 py-1 hover:bg-indigo-50 disabled:opacity-50">
        {discover.isPending ? 'Parsing spec...' : 'Discover actions'}
      </button>

      {error && (
        <p className="flex items-center gap-1 text-xs text-red-600 mt-2">
          <AlertCircle className="h-3.5 w-3.5 shrink-0" />
          {error}
        </p>
      )}

      {result && (
        <div className="mt-3 border-t pt-3">
          {(result.warnings ?? []).map((w, i) => (
            <p key={i} className="flex items-start gap-1 text-[11px] text-amber-700 mb-1">
              <AlertCircle className="h-3 w-3 shrink-0 mt-0.5" />
              <span>{w}</span>
            </p>
          ))}
          {actionEntries.length === 0 ? (
            <p className="text-xs text-gray-400 italic">No actions found in this spec.</p>
          ) : (
            <>
              <p className="text-xs text-gray-500 mb-2">
                {actionEntries.length} action{actionEntries.length === 1 ? '' : 's'} found -- review and select which to add:
              </p>
              <div className="max-h-56 overflow-y-auto space-y-1.5 pr-1">
                {actionEntries.map(([name, a]) => (
                  <label key={name} className="flex items-start gap-2 text-xs cursor-pointer">
                    <input type="checkbox" checked={!!selected[name]}
                      onChange={e => setSelected(s => ({ ...s, [name]: e.target.checked }))}
                      className="mt-0.5" />
                    <span className="flex-1">
                      <span className="font-mono font-medium">{name}</span>{' '}
                      <span className="text-gray-500 font-mono">{a.method} {a.path}</span>
                      {a.summary && <span className="block text-gray-400">{a.summary}</span>}
                      {a.has_path_params && (
                        <span className="flex items-center gap-1 text-amber-700 mt-0.5">
                          <AlertCircle className="h-3 w-3 shrink-0" />
                          Has a path parameter -- won't dispatch correctly until the gateway supports substitution.
                        </span>
                      )}
                    </span>
                  </label>
                ))}
              </div>
              <button type="button" onClick={handleAddSelected}
                disabled={!Object.values(selected).some(Boolean)}
                className="mt-2 text-xs text-indigo-600 hover:text-indigo-800 font-medium disabled:opacity-50 disabled:hover:text-indigo-600">
                + Add selected to action mappings
              </button>
            </>
          )}
        </div>
      )}
    </div>
  )
}

// Add Tool panel

function AddToolPanel({ onClose }: { onClose: () => void }) {
  const create = useCreateTool()
  const [toast, setToast] = useState<string | null>(null)

  const [name, setName]         = useState('')
  const [type, setType]         = useState<ToolType>('mcp')
  const [authType, setAuthType] = useState<'oauth2' | 'api_key' | 'basic' | 'db_connection_string'>('api_key')
  const [mcpCommand, setMcpCommand] = useState('')
  const [mcpArgs, setMcpArgs]   = useState('')
  const [baseUrl, setBaseUrl]   = useState('')
  const [credKey, setCredKey]   = useState('')
  const [connStr, setConnStr]   = useState('')
  const [actionRows, setActionRows] = useState<ActionPathRow[]>([])
  const [provider, setProvider]   = useState<ToolProvider>('claude')
  const [auditMode, setAuditMode] = useState<ToolAuditMode>('structural_metadata_only')
  const [dataHandling, setDataHandling] = useState<ToolDataHandling>('unknown')
  const [dataHandlingNote, setDataHandlingNote] = useState('')

  // Same merge as EditToolPanel's identical handler below -- discovery is
  // stateless (POST /v1/gateway/openapi/discover needs no existing tool
  // ID), so a brand-new tool can populate its action mappings from a spec
  // during creation itself, not only after an extra save-then-reopen round
  // trip (a real gap a code-review finding on this brief caught).
  function handleAddDiscoveredActions(newRows: ActionPathRow[]) {
    setActionRows(prev => {
      const newNames = new Set(newRows.map(r => r.action))
      return [...prev.filter(r => !newNames.has(r.action)), ...newRows]
    })
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    try {
      const actionPaths = type === 'rest_api' ? rowsToActionPaths(actionRows) : {}
      const body: ToolCreateWithActions = {
        name, type, auth_type: authType,
        mcp_command: type === 'mcp' ? mcpCommand || undefined : undefined,
        mcp_args: type === 'mcp' && mcpArgs ? mcpArgs.split(' ').filter(Boolean) : undefined,
        base_url: type === 'rest_api' ? baseUrl || undefined : undefined,
        credentials: {
          api_key: authType === 'api_key' ? credKey || undefined : undefined,
          connection_string: authType === 'db_connection_string' ? connStr || undefined : undefined,
        },
        action_paths: Object.keys(actionPaths).length > 0 ? actionPaths : undefined,
        provider: type === 'ai_provider' ? provider : undefined,
        audit_mode: type === 'ai_provider' ? auditMode : undefined,
        data_handling_designation: type === 'ai_provider' ? dataHandling : undefined,
        data_handling_note: type === 'ai_provider' && dataHandlingNote ? dataHandlingNote : undefined,
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
              <select value={type} onChange={e => {
                  const next = e.target.value as typeof type
                  setType(next)
                  // ai_provider connectors only ever authenticate via
                  // api_key (aiprovider.Credentials/ClaudeAdapter, backend-
                  // enforced too) -- force it here so the API key field is
                  // always visible for this type, instead of leaving a
                  // stale unrelated auth type selected with no way to
                  // notice before a confusing 400 on submit (code review
                  // finding, this task).
                  if (next === 'ai_provider') setAuthType('api_key')
                }}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                <option value="mcp">MCP</option>
                <option value="rest_api">REST API</option>
                <option value="database">Database</option>
                <option value="ai_provider">AI Provider</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Auth type</label>
              <select value={authType} disabled={type === 'ai_provider'} onChange={e => setAuthType(e.target.value as typeof authType)}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 disabled:bg-gray-50 disabled:text-gray-500">
                <option value="api_key">API key</option>
                <option value="oauth2">OAuth 2</option>
                <option value="basic">Basic auth</option>
                <option value="db_connection_string">Connection string</option>
              </select>
              {type === 'ai_provider' && (
                <p className="text-xs text-gray-400 mt-1">AI Provider connectors always authenticate via API key.</p>
              )}
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

          {type === 'rest_api' && (
            <OpenAPIDiscoverySection onAddActions={handleAddDiscoveredActions} />
          )}

          {type === 'rest_api' && (
            <ActionPathsEditor rows={actionRows} onChange={setActionRows} />
          )}

          {type === 'ai_provider' && (
            <>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Provider</label>
                <select value={provider} onChange={e => setProvider(e.target.value as ToolProvider)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  {AI_PROVIDERS.map(p => <option key={p.value} value={p.value}>{p.label}</option>)}
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Audit logging mode</label>
                <select value={auditMode} onChange={e => setAuditMode(e.target.value as ToolAuditMode)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  {AUDIT_MODES.map(m => <option key={m.value} value={m.value}>{m.label}</option>)}
                </select>
                <p className="text-xs text-gray-400 mt-1">{AUDIT_MODES.find(m => m.value === auditMode)?.hint}</p>
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Data handling</label>
                <select value={dataHandling} onChange={e => setDataHandling(e.target.value as ToolDataHandling)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  {DATA_HANDLING_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
                </select>
                <p className="text-xs text-gray-400 mt-1">
                  Visibility only -- EAMI cannot enforce what {AI_PROVIDERS.find(p => p.value === provider)?.label ?? 'the provider'} does with dispatched data. This records what your commercial agreement actually says.
                </p>
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Data handling note (optional)</label>
                <textarea value={dataHandlingNote} onChange={e => setDataHandlingNote(e.target.value)} rows={2}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  placeholder="e.g. Anthropic Enterprise Agreement dated 2026-03-01, ZDR Addendum" />
              </div>
            </>
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

// Edit Tool panel (B-045)

function EditToolPanel({ tool, onClose }: { tool: ToolWithActions; onClose: () => void }) {
  const update = useUpdateTool()
  const [toast, setToast] = useState<string | null>(null)

  const [name, setName]       = useState(tool.name)
  const [mcpCommand, setMcpCommand] = useState(tool.mcp_command ?? '')
  const [mcpArgs, setMcpArgs] = useState('') // display-only default; mcp_args isn't returned by ListTools today
  const [baseUrl, setBaseUrl] = useState(tool.base_url ?? '')
  // Credential field always starts blank -- the current value is never
  // returned by any API response (B-022), so there's nothing to pre-fill.
  // Left blank and submitted, it's simply omitted from the request body,
  // which the backend treats as "keep the existing encrypted value"
  // (AC2) -- not "clear it."
  const [credValue, setCredValue] = useState('')
  const [actionRows, setActionRows] = useState<ActionPathRow[]>(() =>
    Object.entries(tool.action_paths ?? {}).map(([action, m]) => ({ action, path: m.path, method: m.method }))
  )
  const [provider, setProvider]   = useState<ToolProvider>(tool.provider ?? 'claude')
  const [auditMode, setAuditMode] = useState<ToolAuditMode>(tool.audit_mode ?? 'structural_metadata_only')
  const [dataHandling, setDataHandling] = useState<ToolDataHandling>(tool.data_handling_designation ?? 'unknown')
  const [dataHandlingNote, setDataHandlingNote] = useState(tool.data_handling_note ?? '')

  const credLabel =
    tool.auth_type === 'api_key' ? 'API key' :
    tool.auth_type === 'db_connection_string' ? 'Connection string' :
    null

  // Merges newly-discovered rows into actionRows: a discovered action whose
  // name matches an existing row replaces it (re-running discovery refines
  // the same action), everything else is appended. Nothing here writes to
  // the backend -- this is the same local state ActionPathsEditor already
  // edits, saved only when the admin clicks this panel's own Save button.
  function handleAddDiscoveredActions(newRows: ActionPathRow[]) {
    setActionRows(prev => {
      const newNames = new Set(newRows.map(r => r.action))
      return [...prev.filter(r => !newNames.has(r.action)), ...newRows]
    })
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    try {
      const body: ToolUpdate = {
        name,
        mcp_command: tool.type === 'mcp' ? (mcpCommand || undefined) : undefined,
        // trim() first: a whitespace-only value is truthy in JS but
        // .split(' ').filter(Boolean) on it still yields [] -- an
        // explicit empty array, not "omitted" -- which would silently
        // wipe the tool's existing stored args instead of leaving them
        // unchanged (found during B-045's code review).
        mcp_args: tool.type === 'mcp' && mcpArgs.trim() ? mcpArgs.trim().split(/\s+/).filter(Boolean) : undefined,
        base_url: tool.type === 'rest_api' ? (baseUrl || undefined) : undefined,
        // Always sent (even {}) when the tool is rest_api, mirroring how
        // base_url/mcp_command above reflect current form state rather
        // than being diffed against the original -- this is also how an
        // admin explicitly clears all mappings (empty rows -> {} -> the
        // backend's documented "present-but-empty" clear semantics).
        action_paths: tool.type === 'rest_api' ? rowsToActionPaths(actionRows) : undefined,
        provider: tool.type === 'ai_provider' ? provider : undefined,
        audit_mode: tool.type === 'ai_provider' ? auditMode : undefined,
        data_handling_designation: tool.type === 'ai_provider' ? dataHandling : undefined,
        // Deliberately NOT `|| undefined` (unlike base_url above) -- an
        // empty string here is a real, meaningful "clear the note" value
        // the backend distinguishes from "omitted" (see
        // store.UpdateToolParams.DataHandlingNote's doc comment). Using
        // `|| undefined` would make clearing an already-set note
        // impossible through this UI.
        data_handling_note: tool.type === 'ai_provider' ? dataHandlingNote : undefined,
      }
      // Only include `credentials` at all if the admin actually typed
      // something -- an empty/omitted object here must never reach the
      // backend as "clear the credential," and credentialsProvided()
      // server-side treats an omitted field exactly this way.
      if (credValue) {
        body.credentials =
          tool.auth_type === 'api_key' ? { api_key: credValue } :
          tool.auth_type === 'db_connection_string' ? { connection_string: credValue } :
          undefined
      }
      await update.mutateAsync({ id: tool.id, body })
      setToast('Tool updated')
      setTimeout(() => { setToast(null); onClose() }, 1000)
    } catch {
      setToast('Failed to update tool')
    }
  }

  return (
    <div className="fixed inset-y-0 right-0 w-[440px] bg-white shadow-xl flex flex-col z-50 border-l border-gray-200">
      <div className="flex items-center justify-between px-6 py-4 border-b">
        <h2 className="font-semibold text-gray-900">Edit Tool Connection</h2>
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
        <form id="tool-edit-form" onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Tool name</label>
            <input required value={name} onChange={e => setName(e.target.value)}
              className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Type</label>
              <div className="w-full border rounded px-3 py-2 text-sm bg-gray-50 text-gray-500">{tool.type}</div>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Auth type</label>
              <div className="w-full border rounded px-3 py-2 text-sm bg-gray-50 text-gray-500">{tool.auth_type}</div>
            </div>
          </div>
          <p className="text-xs text-gray-400 -mt-2">Type and auth method can't be changed after creation. Remove and re-add the tool to switch either.</p>

          {tool.type === 'mcp' && (
            <>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">MCP command</label>
                <input value={mcpCommand} onChange={e => setMcpCommand(e.target.value)}
                  className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500" />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">MCP args (space-separated)</label>
                <input value={mcpArgs} onChange={e => setMcpArgs(e.target.value)}
                  placeholder="Leave blank to keep current args"
                  className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500" />
              </div>
            </>
          )}

          {tool.type === 'rest_api' && (
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Base URL</label>
              <input value={baseUrl} onChange={e => setBaseUrl(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500" />
            </div>
          )}

          {tool.type === 'rest_api' && (
            <OpenAPIDiscoverySection onAddActions={handleAddDiscoveredActions} />
          )}

          {tool.type === 'rest_api' && (
            <ActionPathsEditor rows={actionRows} onChange={setActionRows} />
          )}

          {tool.type === 'ai_provider' && (
            <>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Provider</label>
                <select value={provider} onChange={e => setProvider(e.target.value as ToolProvider)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  {AI_PROVIDERS.map(p => <option key={p.value} value={p.value}>{p.label}</option>)}
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Audit logging mode</label>
                <select value={auditMode} onChange={e => setAuditMode(e.target.value as ToolAuditMode)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  {AUDIT_MODES.map(m => <option key={m.value} value={m.value}>{m.label}</option>)}
                </select>
                <p className="text-xs text-gray-400 mt-1">{AUDIT_MODES.find(m => m.value === auditMode)?.hint}</p>
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Data handling</label>
                <select value={dataHandling} onChange={e => setDataHandling(e.target.value as ToolDataHandling)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  {DATA_HANDLING_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
                </select>
                {dataHandling === 'unknown' && (
                  <p className="flex items-center gap-1 text-xs text-amber-700 mt-1">
                    <AlertCircle className="h-3.5 w-3.5 shrink-0" />
                    No data-handling designation confirmed for this connector yet.
                  </p>
                )}
                <p className="text-xs text-gray-400 mt-1">
                  Visibility only -- EAMI cannot enforce what {AI_PROVIDERS.find(p => p.value === provider)?.label ?? 'the provider'} does with dispatched data. This records what your commercial agreement actually says.
                </p>
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Data handling note (optional)</label>
                <textarea value={dataHandlingNote} onChange={e => setDataHandlingNote(e.target.value)} rows={2}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  placeholder="e.g. Anthropic Enterprise Agreement dated 2026-03-01, ZDR Addendum" />
              </div>
            </>
          )}

          {credLabel && (
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">{credLabel} (stored encrypted)</label>
              <input type="password" value={credValue} onChange={e => setCredValue(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="Leave blank to keep the current value" />
            </div>
          )}
        </form>
      </div>

      <div className="px-6 py-4 border-t flex gap-3">
        <button type="submit" form="tool-edit-form" disabled={update.isPending}
          className="flex-1 bg-indigo-600 text-white rounded px-4 py-2 text-sm font-medium hover:bg-indigo-700 disabled:opacity-50">
          {update.isPending ? 'Saving...' : 'Save changes'}
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
  const [editTarget, setEditTarget]     = useState<ToolWithActions | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<ToolWithActions | null>(null)
  const [testResult, setTestResult]     = useState<Record<string, { state: TestState; message?: string }>>({})

  const tools: ToolWithActions[] = (data as any)?.data ?? []

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
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-1.5">
                          <TypeBadge type={tool.type} />
                          {tool.type === 'ai_provider' && (tool.data_handling_designation ?? 'unknown') === 'unknown' && (
                            <span title="No data-handling designation confirmed for this connector -- open it to set one."
                              className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[11px] font-medium bg-amber-100 text-amber-800">
                              <AlertCircle className="h-3 w-3" />
                              data handling unknown
                            </span>
                          )}
                        </div>
                      </td>
                      <td className="px-4 py-3 text-xs text-gray-500 font-mono">{tool.auth_type ?? '--'}</td>
                      <td className="px-4 py-3"><StatusBadge status={tool.status} /></td>
                      <td className="px-4 py-3 text-xs font-mono text-gray-500 max-w-xs truncate">
                        {tool.mcp_command ?? tool.base_url ?? tool.provider ?? '--'}
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
                          <button onClick={() => setEditTarget(tool)}
                            className="text-gray-400 hover:text-indigo-600" title="Edit">
                            <Pencil className="h-4 w-4" />
                          </button>
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

      {editTarget && (
        <>
          <div className="fixed inset-0 bg-black/20 z-40" onClick={() => setEditTarget(null)} />
          <EditToolPanel tool={editTarget} onClose={() => setEditTarget(null)} />
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
