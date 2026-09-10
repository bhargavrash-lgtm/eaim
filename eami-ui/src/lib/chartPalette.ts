// Single source of truth for the chart color palette previously
// hand-duplicated in FinOpsPage.tsx (MODEL_COLORS) and PasteEventsPage.tsx
// (DOMAIN_COLORS/FALLBACK_COLORS). These are raw hex, not Tailwind
// classes, because recharts consumes color props as plain CSS color
// values, not className strings.
//
// PasteEventsPage.tsx's domain-color map has one additional color
// (#0ea5e9, used for copilot.microsoft.com) that is NOT part of this
// shared array -- it's genuinely unique to that one assignment, not a
// duplicate, so it stays defined locally in that file rather than being
// folded in here.
export const CHART_PALETTE = [
  '#6366f1',
  '#10b981',
  '#f59e0b',
  '#ef4444',
  '#8b5cf6',
  '#06b6d4',
  '#f97316',
  '#84cc16',
] as const
