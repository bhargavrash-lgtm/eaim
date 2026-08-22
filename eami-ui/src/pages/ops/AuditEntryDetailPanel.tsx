// AuditEntryDetailPanel.tsx -- Audit Log detail slide-out (B-094)
// Owned by FE-Ops
//
// Shows fields already sent by GET /v1/audit but never rendered by the
// table (parameters, full hash/prev_hash, raw agent/policy/approval ids),
// the newly-surfaced data_handling_designation, live-resolved policy/
// approval names, and two DELIBERATELY DISTINCT hash-chain claims -- see
// the CRITICAL REQUIREMENT in this brief: never blur them together.
import { useState, useEffect, type ReactNode } from 'react'
import { Copy, Check, Info, ShieldCheck, ShieldAlert, Loader2 } from 'lucide-react'
import { apiFetch, ApiFetchError } from '@/api/client'
import { useAuthStore } from '@/stores/authStore'
import { useVerifyAuditChainToEntry } from '@/hooks/useAudit'
import type { AuditEntry } from '@/hooks/useAudit'
import type { Policy } from '@/hooks/usePolicies'
import type { ApprovalRequest } from '@/hooks/useApprovals'
import { useQuery } from '@tanstack/react-query'

const DATA_HANDLING_LABELS: Record<string, string> = {
  unknown: 'Unknown / not configured',
  zero_retention: 'Zero Data Retention enabled',
  standard_retention: 'Standard retention (no ZDR)',
}

// ── small shared bits ────────────────────────────────────────────────────

function CopyableValue({ value, mono = true }: { value: string; mono?: boolean }) {
  const [copied, setCopied] = useState(false)
  function handleCopy() {
    navigator.clipboard.writeText(value).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1200)
    }).catch(() => {
      // Clipboard write can be denied (permission, non-secure context) --
      // fail silently rather than an unhandled rejection; the value is
      // still visible and selectable in the panel.
    })
  }
  return (
    <button
      onClick={handleCopy}
      title={copied ? 'Copied!' : 'Copy'}
      className={`group inline-flex items-center gap-1.5 text-left break-all rounded px-1.5 py-0.5 -mx-1.5 hover:bg-gray-100 ${mono ? 'font-mono' : ''}`}
    >
      <span className="text-gray-700">{value}</span>
      {copied
        ? <Check className="h-3 w-3 shrink-0 text-green-600" />
        : <Copy className="h-3 w-3 shrink-0 text-gray-300 group-hover:text-gray-500" />}
    </button>
  )
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <div className="text-xs font-semibold text-gray-500 uppercase mb-1">{label}</div>
      <div className="text-sm">{children}</div>
    </div>
  )
}

// ── related-entity resolution (live GetPolicy/GetApproval, lazy on open) ──

function ResolvedPolicy({ policyId }: { policyId: string }) {
  const { data, isLoading, error, notFound } = usePolicyLookup(policyId)
  if (isLoading) return <span className="text-xs text-gray-400">Resolving policy...</span>
  if (notFound) return <span className="text-xs text-gray-400 italic">Policy no longer exists</span>
  if (error) return <span className="text-xs text-red-500">Failed to load policy</span>
  return (
    <div className="text-sm">
      <div className="font-medium text-gray-900">{data?.name}</div>
      {data?.description && <div className="text-xs text-gray-500 mt-0.5">{data.description}</div>}
      <div className="text-xs text-gray-400 mt-0.5">action: {data?.action} · status: {data?.status}</div>
    </div>
  )
}

// api/openapi.yaml doesn't document a 404 response for GET
// /v1/gateway/policies/{policyId} (200-only), so openapi-fetch's generated
// error type collapses to `never` for this operation and the typed `api`
// client can't be used to detect a real 404 here. apiFetch (the existing
// undocumented/spec-mismatch escape hatch, same category of workaround as
// useReorderPolicies's own precedent) is used instead specifically to read
// ApiFetchError.status.
function usePolicyLookup(policyId: string) {
  const q = useQuery({
    queryKey: ['policy-lookup', policyId],
    queryFn: async () => {
      try {
        return await apiFetch<Policy>(`/v1/gateway/policies/${policyId}`)
      } catch (err) {
        if (err instanceof ApiFetchError && err.status === 404) return null
        throw err
      }
    },
    staleTime: 30_000,
  })
  return { data: q.data ?? undefined, isLoading: q.isLoading, error: q.isError, notFound: q.data === null }
}

function ResolvedApproval({ approvalId }: { approvalId: string }) {
  const { data, isLoading, error, notFound } = useApprovalLookup(approvalId)
  if (isLoading) return <span className="text-xs text-gray-400">Resolving approval...</span>
  if (notFound) return <span className="text-xs text-gray-400 italic">Approval no longer exists</span>
  if (error) return <span className="text-xs text-red-500">Failed to load approval</span>
  return (
    <div className="text-sm">
      <div className="font-medium text-gray-900">{data?.tool_name} &mdash; {data?.action}</div>
      <div className="text-xs text-gray-400 mt-0.5">status: {data?.status} · risk: {data?.risk_level}</div>
    </div>
  )
}

// Same 200-only spec gap as GetPolicy above -- apiFetch used for the same reason.
function useApprovalLookup(approvalId: string) {
  const q = useQuery({
    queryKey: ['approval-lookup', approvalId],
    queryFn: async () => {
      try {
        return await apiFetch<ApprovalRequest>(`/v1/approvals/${approvalId}`)
      } catch (err) {
        if (err instanceof ApiFetchError && err.status === 404) return null
        throw err
      }
    },
    staleTime: 30_000,
  })
  return { data: q.data ?? undefined, isLoading: q.isLoading, error: q.isError, notFound: q.data === null }
}

// ── self-consistency (cheap, local, O(1)) ────────────────────────────────
//
// Recomputes THIS entry's own hash from its own stored fields (including
// its own prev_hash) and compares to the stored hash. This proves only
// that the row hasn't been edited without also recomputing its hash -- it
// says NOTHING about whether an earlier row in the chain was tampered with
// (an attacker with DB write access could trivially recompute this row's
// hash too, after editing it; the real defense is that they'd then also
// have to re-chain every row after it, which is what the full verify
// below actually checks). Never call this "verified" or "chain intact" --
// see the CRITICAL REQUIREMENT in the B-094 task brief.
//
// org_id isn't part of GET /v1/audit's response, but ListAudit is always
// scoped to the caller's own org (WHERE org_id = uc.OrgID) -- so every
// entry a viewer can see belongs to their own session's org_id, already
// known client-side via useAuthStore. Using it here is exact, not a guess.
async function computeSelfConsistency(entry: AuditEntry, orgId: string): Promise<boolean> {
  // Must byte-for-byte match eami-gateway/internal/audit/writer.go's
  // formula, including Go's time.RFC3339 formatting (whole seconds, no
  // fractional digits) -- NOT the full-precision timestamp string the API
  // actually returns.
  const d = new Date(entry.timestamp)
  const pad = (n: number) => String(n).padStart(2, '0')
  const rfc3339Seconds =
    `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}T` +
    `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}Z`

  const content =
    (entry.prev_hash ?? '') + entry.id + orgId + entry.agent_name +
    entry.tool_name + entry.action + entry.decision + rfc3339Seconds

  const bytes = new TextEncoder().encode(content)
  const digest = await crypto.subtle.digest('SHA-256', bytes)
  const hex = Array.from(new Uint8Array(digest)).map(b => b.toString(16).padStart(2, '0')).join('')
  return hex === entry.hash
}

function SelfConsistencyIndicator({ entry }: { entry: AuditEntry }) {
  const orgId = useAuthStore(s => s.user?.org_id)
  const [state, setState] = useState<'checking' | 'consistent' | 'inconsistent' | 'error'>('checking')

  useEffect(() => {
    let cancelled = false
    setState('checking')
    if (!orgId) { setState('error'); return }
    computeSelfConsistency(entry, orgId)
      .then(ok => { if (!cancelled) setState(ok ? 'consistent' : 'inconsistent') })
      .catch(() => { if (!cancelled) setState('error') })
    return () => { cancelled = true }
  }, [entry, orgId])

  return (
    <div className="rounded border border-gray-200 bg-gray-50 px-3 py-2.5">
      <div className="flex items-center gap-2 text-sm font-medium text-gray-700">
        <Info className="h-4 w-4 text-gray-400 shrink-0" />
        {state === 'checking' && 'Checking self-consistency...'}
        {state === 'consistent' && 'Self-consistent (this entry only)'}
        {state === 'inconsistent' && 'NOT self-consistent -- this entry\'s stored hash does not match its own recorded fields'}
        {state === 'error' && 'Could not run the self-consistency check'}
      </div>
      <p className="text-xs text-gray-500 mt-1 pl-6">
        This checks only that this entry's own hash recomputes correctly from its own recorded fields.
        It does <strong>not</strong> confirm that no earlier row in the chain was tampered with -- for that, use
        "Verify chain to this entry" below.
      </p>
    </div>
  )
}

// ── full chain verification (real backend call, on demand) ──────────────

function FullChainVerification({ entry }: { entry: AuditEntry }) {
  const verify = useVerifyAuditChainToEntry()

  return (
    <div className="rounded border border-indigo-200 bg-indigo-50/40 px-3 py-3">
      <div className="text-sm font-semibold text-gray-800 mb-1">Full Chain Verification</div>
      <p className="text-xs text-gray-500 mb-2">
        Walks the real hash chain from the very first audit entry through this one and recomputes every hash.
        This is the only check here that actually confirms no earlier row has been tampered with.
      </p>
      <button
        onClick={() => verify.mutate(entry.timestamp)}
        disabled={verify.isPending}
        className="flex items-center gap-1.5 bg-indigo-600 text-white rounded px-3 py-1.5 text-sm font-medium hover:bg-indigo-700 disabled:opacity-50"
      >
        {verify.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
        Verify chain to this entry
      </button>

      {verify.isSuccess && verify.data && (
        <div className={`mt-3 rounded px-3 py-2 text-sm flex items-start gap-2 ${
          verify.data.valid ? 'bg-green-50 border border-green-200 text-green-800' : 'bg-red-50 border border-red-200 text-red-800'
        }`}>
          {verify.data.valid
            ? <ShieldCheck className="h-4 w-4 shrink-0 mt-0.5" />
            : <ShieldAlert className="h-4 w-4 shrink-0 mt-0.5" />}
          <div>
            <div className="font-medium">
              {verify.data.valid
                ? `Chain verified -- genesis through this entry (${verify.data.total_rows.toLocaleString()} rows checked)`
                : 'Chain broken'}
            </div>
            <div className="text-xs mt-0.5 opacity-90">{verify.data.message}</div>
          </div>
        </div>
      )}
      {verify.isError && (
        <div className="mt-3 rounded px-3 py-2 text-sm bg-red-50 border border-red-200 text-red-800">
          Could not reach the verification endpoint. Try again.
        </div>
      )}
    </div>
  )
}

// ── main panel ────────────────────────────────────────────────────────────

export function AuditEntryDetailPanel({ entry, onClose }: { entry: AuditEntry; onClose: () => void }) {
  return (
    <div className="fixed inset-y-0 right-0 w-[480px] bg-white shadow-xl flex flex-col z-50 border-l border-gray-200">
      <div className="flex items-center justify-between px-6 py-4 border-b">
        <h2 className="font-semibold text-gray-900">Audit Entry Detail</h2>
        <button onClick={onClose} className="text-gray-400 hover:text-gray-600 text-xl leading-none">x</button>
      </div>

      <div className="flex-1 overflow-y-auto px-6 py-4 space-y-5">
        <Field label="Timestamp">
          <span className="font-mono text-xs text-gray-700">{entry.timestamp}</span>
        </Field>

        <div className="grid grid-cols-2 gap-4">
          <Field label="Agent">
            <div>{entry.agent_name}</div>
            {entry.agent_id && <div className="mt-1"><CopyableValue value={entry.agent_id} /></div>}
          </Field>
          <Field label="Tool / Action">
            <div className="font-mono text-xs">{entry.tool_name}</div>
            <div className="text-gray-600">{entry.action}</div>
          </Field>
        </div>

        <Field label="Decision">
          <span>{entry.decision}{entry.approved_by ? ` (by ${entry.approved_by})` : ''}</span>
        </Field>

        {entry.data_handling_designation && (
          <Field label="Data handling (at time of dispatch)">
            {DATA_HANDLING_LABELS[entry.data_handling_designation] ?? entry.data_handling_designation}
          </Field>
        )}

        <Field label="Parameters">
          {entry.parameters && Object.keys(entry.parameters as object).length > 0 ? (
            <pre className="text-xs font-mono bg-gray-50 border border-gray-200 rounded p-3 overflow-x-auto whitespace-pre-wrap break-all">
              {JSON.stringify(entry.parameters, null, 2)}
            </pre>
          ) : (
            <span className="text-gray-400 italic">No parameters recorded</span>
          )}
        </Field>

        {entry.policy_id && (
          <Field label="Policy">
            <ResolvedPolicy policyId={entry.policy_id} />
            <div className="mt-1"><CopyableValue value={entry.policy_id} /></div>
          </Field>
        )}

        {entry.approval_id && (
          <Field label="Approval">
            <ResolvedApproval approvalId={entry.approval_id} />
            <div className="mt-1"><CopyableValue value={entry.approval_id} /></div>
          </Field>
        )}

        <Field label="Hash">
          <CopyableValue value={entry.hash} />
        </Field>
        <Field label="Previous hash">
          <CopyableValue value={entry.prev_hash ?? '(none recorded)'} />
        </Field>

        <div className="pt-2 border-t space-y-3">
          <SelfConsistencyIndicator entry={entry} />
          <FullChainVerification entry={entry} />
        </div>
      </div>
    </div>
  )
}
