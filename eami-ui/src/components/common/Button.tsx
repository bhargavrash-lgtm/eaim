import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { RefreshCw } from 'lucide-react'

type ButtonVariant = 'primary' | 'secondary' | 'outline' | 'destructive'
type ButtonSize = 'md' | 'sm'

interface ButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'className'> {
  variant?: ButtonVariant
  size?: ButtonSize
  isLoading?: boolean
  className?: string
  children: ReactNode
}

const VARIANT_STYLES: Record<ButtonVariant, string> = {
  primary: 'bg-brand-600 text-white hover:bg-brand-700',
  secondary: 'text-gray-600 hover:text-gray-900',
  outline: 'border border-gray-300 text-gray-700 hover:bg-gray-50',
  destructive: 'bg-red-600 text-white hover:bg-red-700',
}

// sm exists for the one real compact inline action found (ToolsPage.tsx's
// "Discover actions") -- not speculative, added because a real existing
// button needed it, matching this session's own established discipline
// of not adding an unused option "just in case".
const SIZE_STYLES: Record<ButtonSize, string> = {
  md: 'px-4 py-2 text-sm',
  sm: 'px-2.5 py-1 text-xs',
}

// Single shared button, closing the found split between icon-spinner and
// text-swap-only loading conventions (11 text-swap-only instances found
// vs. a handful of correct icon-spinner ones) and the found gap where a
// form's "Cancel" button was never disabled while its sibling submit
// button was mid-flight (ConfirmDialog's own B-091 fix disabled both of
// its buttons; that discipline never propagated past ConfirmDialog
// itself). variant="primary" uses the real brand.600 token, closing the
// bg-indigo-600-vs-bg-brand-600 drift B-176 found alongside this pattern
// -- not a separate fix bundled in by accident, the audit named these
// together.
export function Button({
  variant = 'primary',
  size = 'md',
  isLoading = false,
  disabled,
  className = '',
  children,
  ...rest
}: ButtonProps) {
  return (
    <button
      {...rest}
      disabled={disabled || isLoading}
      className={`inline-flex items-center justify-center gap-1.5 rounded-md font-medium disabled:opacity-50 disabled:cursor-not-allowed ${SIZE_STYLES[size]} ${VARIANT_STYLES[variant]} ${className}`}
    >
      {isLoading && <RefreshCw className="h-3.5 w-3.5 animate-spin" />}
      {children}
    </button>
  )
}
