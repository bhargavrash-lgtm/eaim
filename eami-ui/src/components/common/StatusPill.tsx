interface StatusPillProps {
  status: 'active' | 'suspended' | 'revoked' | 'connected' | 'degraded' | 'disconnected'
}

const STATUS_STYLES: Record<StatusPillProps['status'], string> = {
  active: 'bg-green-100 text-green-800',
  connected: 'bg-green-100 text-green-800',
  suspended: 'bg-amber-100 text-amber-800',
  degraded: 'bg-amber-100 text-amber-800',
  revoked: 'bg-red-100 text-red-800',
  disconnected: 'bg-red-100 text-red-800',
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
