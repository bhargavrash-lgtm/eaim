// Endpoint formatting helpers, shared by Assets' endpoint view and Endpoint
// Detail (B-252 C2/C3; first extracted from Discover in C0).

// ── Formatting helpers ──────────────────────────────────────────────────────

export function formatBytes(bytes: number | null | undefined): string {
  if (bytes == null) return '—'
  if (bytes >= 1_073_741_824) return `${(bytes / 1_073_741_824).toFixed(1)} GB`
  if (bytes >= 1_048_576) return `${(bytes / 1_048_576).toFixed(0)} MB`
  return `${bytes} B`
}

// The platform values eami-agent reports (runtime.GOOS) as display labels.
const OS_LABEL: Record<string, string> = { windows: 'Windows', linux: 'Linux', darwin: 'macOS' }
export function formatOS(os: string | null | undefined): string {
  if (!os) return '—'
  return OS_LABEL[os] ?? os
}

export function formatRelativeTime(iso: string): string {
  const secs = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (secs < 60) return `${secs}s ago`
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`
  return `${Math.floor(secs / 86400)}d ago`
}
