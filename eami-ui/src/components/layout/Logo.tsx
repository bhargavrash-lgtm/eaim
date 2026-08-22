import { branding } from '@/branding/config'

interface LogoProps {
  variant?: 'full' | 'mark'
  className?: string
}

/**
 * Reads logo + theme from branding/config.ts -- nothing here is
 * hardcoded. `variant="mark"` is a placeholder monogram (see
 * branding/config.ts and BUILT.md): the source logo is a wordmark only,
 * with no separate icon-only asset to render small, so the mark is
 * generated from the same extracted brand colors instead of faked from
 * the wordmark's pixels.
 */
export function Logo({ variant = 'full', className }: LogoProps) {
  if (variant === 'mark') {
    return (
      <svg viewBox="0 0 32 32" className={className ?? 'h-8 w-8'} role="img" aria-label={branding.displayName}>
        <rect width="32" height="32" rx="7" fill={branding.theme.brand[600]} />
        <text
          x="16"
          y="23"
          fontFamily="ui-sans-serif, system-ui, sans-serif"
          fontSize="19"
          fontWeight="700"
          fill={branding.theme.brand[50]}
          textAnchor="middle"
        >
          r
        </text>
      </svg>
    )
  }

  return (
    <img
      src={branding.logo.full}
      alt={branding.displayName}
      className={className ?? 'h-6 w-auto'}
    />
  )
}
