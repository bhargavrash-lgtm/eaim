interface LoadingSpinnerProps {
  size?: 'sm' | 'md' | 'lg'
}

const SIZE_CLASSES: Record<NonNullable<LoadingSpinnerProps['size']>, string> = {
  sm: 'h-4 w-4 border-2',
  md: 'h-8 w-8 border-2',
  lg: 'h-12 w-12 border-4',
}

export function LoadingSpinner({ size = 'md' }: LoadingSpinnerProps) {
  return (
    <div
      className={`animate-spin rounded-full border-gray-300 border-t-brand-600 ${SIZE_CLASSES[size]}`}
      role="status"
      aria-label="Loading"
    />
  )
}
