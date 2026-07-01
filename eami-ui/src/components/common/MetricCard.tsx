import { TrendingUp, TrendingDown, Minus } from 'lucide-react'

interface MetricCardProps {
  label: string
  value: string | number
  trend?: string
  trendDir?: 'up' | 'down' | 'neutral'
  loading?: boolean
}

export function MetricCard({ label, value, trend, trendDir = 'neutral', loading }: MetricCardProps) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
      {loading ? (
        <div className="animate-pulse space-y-2">
          <div className="h-3 w-24 rounded bg-gray-200" />
          <div className="h-7 w-16 rounded bg-gray-200" />
          <div className="h-3 w-32 rounded bg-gray-200" />
        </div>
      ) : (
        <>
          <p className="text-xs font-medium uppercase tracking-wide text-gray-500">{label}</p>
          <p className="mt-1 text-2xl font-bold text-gray-900">{value}</p>
          {trend && (
            <div className="mt-1 flex items-center gap-1 text-xs">
              {trendDir === 'up' && <TrendingUp className="h-3 w-3 text-green-600" />}
              {trendDir === 'down' && <TrendingDown className="h-3 w-3 text-red-500" />}
              {trendDir === 'neutral' && <Minus className="h-3 w-3 text-gray-400" />}
              <span
                className={
                  trendDir === 'up'
                    ? 'text-green-600'
                    : trendDir === 'down'
                    ? 'text-red-500'
                    : 'text-gray-500'
                }
              >
                {trend}
              </span>
            </div>
          )}
        </>
      )}
    </div>
  )
}
