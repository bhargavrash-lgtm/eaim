interface RiskPillProps {
  tier: 'low' | 'medium' | 'high' | 'critical'
}

const TIER_STYLES: Record<RiskPillProps['tier'], string> = {
  low: 'bg-status-success text-status-success-text',
  medium: 'bg-status-warning text-status-warning-text',
  high: 'bg-status-danger text-status-danger-text',
  critical: 'bg-red-200 text-red-900',
}

export function RiskPill({ tier }: RiskPillProps) {
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium capitalize ${TIER_STYLES[tier]}`}
    >
      {tier}
    </span>
  )
}
