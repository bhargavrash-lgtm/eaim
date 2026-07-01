import { useState } from 'react'
import { CheckCircle } from 'lucide-react'
import {
  PageHeader,
  DataTable,
  ConfirmDialog,
  EmptyState,
  LoadingSpinner,
  RiskPill,
} from '@/components/common'
import {
  useApprovals,
  useDecideApproval,
  type ApprovalRequest,
} from '@/hooks/useApprovals'

// ── Helpers ───────────────────────────────────────────────────────────────────

function formatTimeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const mins = Math.floor(diff / 60_000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins} min ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  return `${Math.floor(hrs / 24)}d ago`
}

function formatExpiry(iso: string): string {
  const secs = Math.max(0, Math.floor((new Date(iso).getTime() - Date.now()) / 1_000))
  const m = Math.floor(secs / 60)
  const s = secs % 60
  return `${m}:${String(s).padStart(2, '0')}`
}

const RISK_BORDER: Record<string, string> = {
  critical: 'border-l-red-500',
  high: 'border-l-red-400',
  medium: 'border-l-amber-400',
  low: 'border-l-transparent',
}

type Tab = 'pending' | 'all'

// ── ApprovalCard — the core of the Pending tab ───────────────────────────────

interface CardProps {
  approval: ApprovalRequest
  isDeciding: boolean
  onDecide: (approval: ApprovalRequest, decision: 'approved' | 'denied') => void
}

function ApprovalCard({ approval, isDeciding, onDecide }: CardProps) {
  const border = RISK_BORDER[approval.risk_level] ?? 'border-l-transparent'

  return (
    <div
      className={`bg-white rounded-lg border border-gray-200 border-l-4 ${border} shadow-sm p-5 space-y-4`}
    >
      {/* Header row */}
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <p className="text-sm font-semibold text-gray-900">
            {approval.agent_name}{' '}
            <span className="font-normal text-gray-500">→ {approval.tool_name}</span>
          </p>
          <p className="mt-0.5 text-sm text-gray-700 break-words">{approval.action}</p>
        </div>
        <div className="flex items-center gap-3 shrink-0">
          <RiskPill tier={approval.risk_level} />
          <span className="text-xs text-gray-400 whitespace-nowrap">
            {formatTimeAgo(approval.created_at)}
          </span>
        </div>
      </div>

      {/* Agent justification — NEVER truncated (project requirement) */}
      <div className="border-l-2 border-indigo-300 pl-3 bg-indigo-50/50 rounded-r-sm py-2 pr-3">
        <p className="text-xs font-medium text-indigo-600 mb-1">Agent's justification</p>
        <p className="text-sm text-gray-700 leading-relaxed font-mono whitespace-pre-wrap break-words">
          {approval.justification}
        </p>
      </div>

      {/* Blast radius chips */}
      {approval.blast_radius && (
        <div className="flex flex-wrap gap-2 text-xs">
          {approval.blast_radius.estimated_records != null && (
            <span className="bg-gray-100 text-gray-700 px-2 py-0.5 rounded">
              ~{approval.blast_radius.estimated_records.toLocaleString()} records
            </span>
          )}
          <span
            className={`px-2 py-0.5 rounded font-medium ${
              approval.blast_radius.reversible
                ? 'bg-green-100 text-green-800'
                : 'bg-red-100 text-red-800'
            }`}
          >
            {approval.blast_radius.reversible ? 'Reversible' : 'Irreversible'}
          </span>
          {approval.blast_radius.environment && (
            <span
              className={`px-2 py-0.5 rounded font-medium ${
                approval.blast_radius.environment === 'production'
                  ? 'bg-red-100 text-red-700'
                  : approval.blast_radius.environment === 'staging'
                  ? 'bg-amber-100 text-amber-700'
                  : 'bg-green-100 text-green-700'
              }`}
            >
              {approval.blast_radius.environment}
            </span>
          )}
        </div>
      )}

      {/* Action row */}
      <div className="flex items-center justify-between pt-1 border-t border-gray-100">
        <span className="text-xs text-gray-400">
          {approval.expires_at ? `Expires in ${formatExpiry(approval.expires_at)}` : ''}
        </span>
        <div className="flex gap-2">
          <button
            disabled={isDeciding}
            onClick={() => onDecide(approval, 'approved')}
            className="px-4 py-1.5 text-xs font-semibold rounded bg-green-600 text-white hover:bg-green-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            Approve
          </button>
          <button
            disabled={isDeciding}
            onClick={() => onDecide(approval, 'denied')}
            className="px-4 py-1.5 text-xs font-semibold rounded bg-red-600 text-white hover:bg-red-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            Deny
          </button>
        </div>
      </div>
    </div>
  )
}

// ── All-approvals table (read-only) ───────────────────────────────────────────

const STATUS_STYLES: Record<string, string> = {
  approved: 'bg-green-100 text-green-800',
  denied: 'bg-red-100 text-red-800',
  pending: 'bg-amber-100 text-amber-700',
  expired: 'bg-gray-100 text-gray-500',
  cancelled: 'bg-gray-100 text-gray-500',
}

const ALL_COLUMNS = [
  {
    key: 'agent_name',
    header: 'Agent',
    render: (row: ApprovalRequest) => (
      <span className="font-medium text-gray-900">{row.agent_name}</span>
    ),
  },
  {
    key: 'tool_name',
    header: 'Tool',
    render: (row: ApprovalRequest) => row.tool_name,
  },
  {
    key: 'action',
    header: 'Action',
    render: (row: ApprovalRequest) => (
      <span className="text-sm text-gray-700 max-w-xs truncate block" title={row.action}>
        {row.action}
      </span>
    ),
  },
  {
    key: 'risk_level',
    header: 'Risk',
    render: (row: ApprovalRequest) => <RiskPill tier={row.risk_level} />,
  },
  {
    key: 'status',
    header: 'Status',
    render: (row: ApprovalRequest) => (
      <span
        className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${STATUS_STYLES[row.status] ?? 'bg-gray-100 text-gray-600'}`}
      >
        {row.status}
      </span>
    ),
  },
  {
    key: 'approved_by',
    header: 'Decided by',
    render: (row: ApprovalRequest) => (
      <span className="text-sm text-gray-500">{row.approved_by ?? '—'}</span>
    ),
  },
  {
    key: 'created_at',
    header: 'Requested',
    render: (row: ApprovalRequest) => (
      <span className="text-sm text-gray-400">{formatTimeAgo(row.created_at)}</span>
    ),
  },
]

// ── Page ─────────────────────────────────────────────────────────────────────

export default function ApprovalsPage() {
  const [tab, setTab] = useState<Tab>('pending')
  const [allPage, setAllPage] = useState(1)

  const [confirmState, setConfirmState] = useState<{
    approval: ApprovalRequest
    decision: 'approved' | 'denied'
  } | null>(null)

  // Inline error banner — shown on decide failure (e.g. 409 double-decide)
  const [decideError, setDecideError] = useState<string | null>(null)

  const pendingQuery = useApprovals({ status: 'pending' })
  const allQuery = useApprovals({ page: allPage, per_page: 25 })

  const decideApproval = useDecideApproval()

  const pendingData = pendingQuery.data?.data ?? []
  const pendingTotal = pendingQuery.data?.meta?.total ?? 0
  const allData = allQuery.data?.data ?? []
  const allTotal = allQuery.data?.meta?.total ?? 0

  function handleDecide(approval: ApprovalRequest, decision: 'approved' | 'denied') {
    setDecideError(null)
    setConfirmState({ approval, decision })
  }

  function handleConfirm() {
    if (!confirmState) return
    decideApproval.mutate(
      { id: confirmState.approval.id, decision: confirmState.decision, reason: '' },
      {
        onSuccess: () => setConfirmState(null),
        onError: (err) => {
          setConfirmState(null)
          const msg = err instanceof Error ? err.message : 'Unknown error'
          setDecideError(`Could not decide: ${msg}`)
        },
      },
    )
  }

  const tabs: { id: Tab; label: string }[] = [
    { id: 'pending', label: `Pending${pendingTotal > 0 ? ` (${pendingTotal})` : ''}` },
    { id: 'all', label: 'All' },
  ]

  return (
    <div className="space-y-6">
      <PageHeader
        title="Approvals"
        subtitle="Review and act on agent approval requests"
      />

      {/* Tab bar */}
      <div className="border-b border-gray-200">
        <nav className="-mb-px flex gap-6">
          {tabs.map((t) => (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              className={`pb-3 text-sm font-medium border-b-2 transition-colors ${
                tab === t.id
                  ? 'border-indigo-600 text-indigo-600'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300'
              }`}
            >
              {t.label}
            </button>
          ))}
        </nav>
      </div>

      {/* ── Pending tab ────────────────────────────────────────────────────── */}
      {tab === 'pending' && (
        <>
          {/* Error banner — dismissible, shown on decide failure */}
          {decideError && (
            <div className="flex items-start gap-3 rounded-md bg-red-50 border border-red-200 px-4 py-3 text-sm text-red-800">
              <span className="flex-1">{decideError}</span>
              <button
                onClick={() => setDecideError(null)}
                className="shrink-0 text-red-500 hover:text-red-700 text-base leading-none"
                aria-label="Dismiss"
              >
                ×
              </button>
            </div>
          )}

          {pendingQuery.isLoading ? (
            <div className="flex justify-center py-16">
              <LoadingSpinner size="lg" />
            </div>
          ) : pendingData.length === 0 ? (
            <EmptyState
              icon={<CheckCircle className="h-8 w-8" />}
              title="No pending approvals"
              description="The queue is clear."
            />
          ) : (
            <div className="space-y-4">
              {pendingData.map((approval) => (
                <ApprovalCard
                  key={approval.id}
                  approval={approval}
                  isDeciding={decideApproval.isPending}
                  onDecide={handleDecide}
                />
              ))}
            </div>
          )}
        </>
      )}

      {/* ── All tab ────────────────────────────────────────────────────────── */}
      {tab === 'all' && (
        <>
          {allQuery.isLoading ? (
            <div className="flex justify-center py-16">
              <LoadingSpinner size="lg" />
            </div>
          ) : allData.length === 0 ? (
            <EmptyState
              title="No approvals yet"
              description="Approval requests from agents will appear here."
            />
          ) : (
            <DataTable
              columns={ALL_COLUMNS}
              data={allData}
              loading={allQuery.isFetching}
              pagination={{
                page: allPage,
                perPage: 25,
                total: allTotal,
                onPageChange: setAllPage,
              }}
            />
          )}
        </>
      )}

      {/* ── Confirm dialog ─────────────────────────────────────────────────── */}
      {confirmState && (
        <ConfirmDialog
          open={true}
          title={
            confirmState.decision === 'approved'
              ? 'Approve this action?'
              : 'Deny this action?'
          }
          description={
            confirmState.decision === 'approved'
              ? 'The agent will immediately execute the action.'
              : 'The agent will be notified that this action was denied.'
          }
          confirmLabel={confirmState.decision === 'approved' ? 'Approve' : 'Deny'}
          destructive={confirmState.decision === 'denied'}
          isLoading={decideApproval.isPending}
          onConfirm={handleConfirm}
          onCancel={() => setConfirmState(null)}
        />
      )}
    </div>
  )
}
