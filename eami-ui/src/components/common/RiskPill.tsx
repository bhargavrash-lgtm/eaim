interface RiskPillProps {
  tier: 'low' | 'medium' | 'high' | 'critical'
}

const TIER_STYLES: Record<RiskPillProps['tier'], string> = {
  low: 'bg-green-100 text-green-800',
  medium: 'bg-amber-100 text-amber-800',
  high: 'bg-red-100 text-red-800',
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
