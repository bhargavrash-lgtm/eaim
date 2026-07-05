// NodesPage.tsx -- Gateway / Nodes
// Owned by FE-Gateway
import { useState } from 'react'
import { Trash2, RefreshCw, Cpu, Activity, Clock, Server } from 'lucide-react'
import {
  PageHeader,
  ConfirmDialog,
  EmptyState,
  LoadingSpinner,
} from '@/components/common'
import { useNodes, useDeleteNode } from '@/hooks/useNodes'
import type { GatewayNode } from '@/hooks/useNodes'

// Badges

const STATUS_STYLES: Record<string, string> = {
  healthy:  'bg-green-100 text-green-800',
  degraded: 'bg-amber-100 text-amber-800',
  standby:  'bg-blue-100 text-blue-700',
  offline:  'bg-red-100 text-red-800',
}

const ROLE_STYLES: Record<string, string> = {
  primary:    'bg-indigo-100 text-indigo-800',
  edge:       'bg-purple-100 text-purple-800',
  dr_standby: 'bg-gray-100 text-gray-600',
}

function StatusBadge({ status }: { status: string }) {
  const dotColor = status === 'healthy' ? 'bg-green-500' : status === 'degraded' ? 'bg-amber-400' : status === 'standby' ? 'bg-blue-400' : 'bg-red-500'
  return (
    <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium capitalize ${STATUS_STYLES[status] ?? 'bg-gray-100 text-gray-600'}`}>
      <span className={`mr-1.5 h-1.5 w-1.5 rounded-full inline-block ${dotColor}`} />
      {status}
    </span>
  )
}

function RoleBadge({ role }: { role: string }) {
  return (
    <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${ROLE_STYLES[role] ?? 'bg-gray-100 text-gray-600'}`}>
      {role.replace('_', ' ')}
    </span>
  )
}

// Helpers

function formatUptime(seconds?: number | null): string {
  if (seconds == null) return '--'
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return d + 'd ' + h + 'h'
  if (h > 0) return h + 'h ' + m + 'm'
  return m + 'm'
}

function formatHeartbeat(iso: string): string {
  const secs = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (secs < 30) return 'just now'
  if (secs < 120) return secs + 's ago'
  return Math.floor(secs / 60) + 'm ago'
}

function CpuBar({ pct }: { pct?: number | null }) {
  if (pct == null) return <span className="text-xs text-gray-400">--</span>
  const color = pct > 85 ? 'bg-red-500' : pct > 60 ? 'bg-amber-400' : 'bg-green-500'
  return (
    <div className="flex items-center gap-2">
      <div className="w-16 h-1.5 bg-gray-200 rounded-full overflow-hidden">
        <div className={`h-full rounded-full ${color}`} style={{ width: Math.min(pct, 100) + '%' }} />
      </div>
      <span className="text-xs text-gray-600">{pct.toFixed(1)}%</span>
    </div>
  )
}

// Node card

interface NodeCardProps {
  node: GatewayNode
  onDelete: (node: GatewayNode) => void
}

function NodeCard({ node, onDelete }: NodeCardProps) {
  return (
    <div className="bg-white rounded-lg border border-gray-200 p-5 space-y-4">
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-2">
          <Server className="h-4 w-4 text-gray-400 shrink-0" />
          <div>
            <p className="font-semibold text-gray-900">{node.name}</p>
            <p className="text-xs text-gray-400 font-mono">{node.address}</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <RoleBadge role={node.role} />
          <StatusBadge status={node.status} />
        </div>
      </div>

      <div className="grid grid-cols-3 gap-4 text-sm">
        <div>
          <div className="flex items-center gap-1 text-xs text-gray-500 mb-1">
            <Cpu className="h-3 w-3" /> CPU
          </div>
          <CpuBar pct={node.cpu_pct} />
        </div>
        <div>
          <div className="flex items-center gap-1 text-xs text-gray-500 mb-1">
            <Activity className="h-3 w-3" /> Req/min
          </div>
          <span className="text-sm font-medium text-gray-800">
            {node.requests_per_min != null ? node.requests_per_min.toLocaleString() : '--'}
          </span>
        </div>
        <div>
          <div className="flex items-center gap-1 text-xs text-gray-500 mb-1">
            <Clock className="h-3 w-3" /> Uptime
          </div>
          <span className="text-sm text-gray-700">{formatUptime(node.uptime_seconds)}</span>
        </div>
      </div>

      <div className="flex items-center justify-between pt-2 border-t border-gray-100">
        <div className="text-xs text-gray-400">
          {node.version && <span className="mr-3 font-mono">v{node.version}</span>}
          {node.hostname && <span>{node.hostname}</span>}
        </div>
        <div className="flex items-center gap-3">
          <span className="text-xs text-gray-400">Heartbeat {node.last_heartbeat ? formatHeartbeat(node.last_heartbeat) : '--'}</span>
          {node.role !== 'primary' && (
            <button onClick={() => onDelete(node)} className="text-gray-400 hover:text-red-600" title="Remove from mesh">
              <Trash2 className="h-4 w-4" />
            </button>
          )}
        </div>
      </div>

      {node.replication_lag_ms != null && (
        <div className={`text-xs px-2 py-1 rounded ${node.replication_lag_ms > 500 ? 'bg-amber-50 text-amber-700' : 'bg-gray-50 text-gray-500'}`}>
          Replication lag: {node.replication_lag_ms} ms
        </div>
      )}
    </div>
  )
}

// Main page

export function NodesPage() {
  const { data, isLoading, error, isFetching, refetch } = useNodes()
  const deleteNode = useDeleteNode()
  const [deleteTarget, setDeleteTarget] = useState<GatewayNode | null>(null)

  const nodes: GatewayNode[] = (data as any)?.data ?? []

  if (isLoading) return <div className="p-6"><LoadingSpinner /></div>
  if (error)    return <div className="p-6 text-sm text-red-500">Failed to load nodes.</div>

  const primary = nodes.filter(n => n.role === 'primary')
  const edge    = nodes.filter(n => n.role === 'edge')
  const standby = nodes.filter(n => n.role === 'dr_standby')

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title="Gateway Nodes"
        subtitle="Serf mesh cluster -- refreshes every 15 s"
        actions={
          <button
            onClick={() => refetch()}
            className={'flex items-center gap-1.5 border border-gray-300 rounded px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-50' + (isFetching ? ' opacity-50 cursor-wait' : '')}
          >
            <RefreshCw className={'h-4 w-4' + (isFetching ? ' animate-spin' : '')} />
            Refresh
          </button>
        }
      />

      <div className="flex-1 overflow-auto p-6 space-y-6">
        {nodes.length === 0 ? (
          <EmptyState
            title="No nodes in mesh"
            description="Start an eami-gateway instance to register it in the Serf cluster."
          />
        ) : (
          <>
            {primary.length > 0 && (
              <section>
                <h2 className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-3">Primary</h2>
                <div className="grid grid-cols-1 gap-4 lg:grid-cols-2 xl:grid-cols-3">
                  {primary.map(n => <NodeCard key={n.id} node={n} onDelete={setDeleteTarget} />)}
                </div>
              </section>
            )}
            {edge.length > 0 && (
              <section>
                <h2 className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-3">Edge nodes</h2>
                <div className="grid grid-cols-1 gap-4 lg:grid-cols-2 xl:grid-cols-3">
                  {edge.map(n => <NodeCard key={n.id} node={n} onDelete={setDeleteTarget} />)}
                </div>
              </section>
            )}
            {standby.length > 0 && (
              <section>
                <h2 className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-3">DR standby</h2>
                <div className="grid grid-cols-1 gap-4 lg:grid-cols-2 xl:grid-cols-3">
                  {standby.map(n => <NodeCard key={n.id} node={n} onDelete={setDeleteTarget} />)}
                </div>
              </section>
            )}
          </>
        )}
      </div>

      {deleteTarget && (
        <ConfirmDialog
          open
          title={'Remove "' + deleteTarget.name + '" from mesh?'}
          description="The node will be deregistered from the Serf cluster. Traffic will failover to remaining nodes. The node process itself will not be stopped."
          confirmLabel="Remove"
          destructive
          isLoading={deleteNode.isPending}
          onConfirm={() => { deleteNode.mutate(deleteTarget.id, { onSuccess: () => setDeleteTarget(null) }) }}
          onCancel={() => setDeleteTarget(null)}
                />
      )}
    </div>
  )
}
