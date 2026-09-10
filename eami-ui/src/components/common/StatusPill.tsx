interface StatusPillProps {
  status: 'active' | 'suspended' | 'revoked' | 'connected' | 'degraded' | 'disconnected'
}

const STATUS_STYLES: Record<StatusPillProps['status'], string> = {
  active: 'bg-status-success text-status-success-text',
  connected: 'bg-status-success text-status-success-text',
  suspended: 'bg-status-warning text-status-warning-text',
  degraded: 'bg-status-warning text-status-warning-text',
  revoked: 'bg-status-danger text-status-danger-text',
  disconnected: 'bg-status-danger text-status-danger-text',
}

export function StatusPill({ status }: StatusPillProps) {
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium capitalize ${STATUS_STYLES[status]}`}
    >
      {status}
    </span>
  )
}
