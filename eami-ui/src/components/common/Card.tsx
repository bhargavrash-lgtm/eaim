import type { ReactNode } from 'react'

interface CardProps {
  className?: string
  children: ReactNode
}

// Deliberately minimal: only `border bg-white` is genuinely shared across
// every card-shell instance found in the audit -- radius, padding, shadow,
// and border color all vary by context (an auth-shell card vs. a compact
// content card vs. a conditionally-red-bordered flagged card), so baking
// any of those in as defaults would either force a visual change on some
// callers or create Tailwind utility-class conflicts with whatever a
// caller supplies via `className`. Callers own their own rounded-*/p-*/
// shadow-*/border-* via `className`.
export function Card({ className = '', children }: CardProps) {
  return <div className={`border bg-white ${className}`}>{children}</div>
}
