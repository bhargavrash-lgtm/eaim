// Honest per-scanner state for an endpoint's detection categories
// (master-sequence item 4). A bare "0" / "None detected" used to mean any of:
// never reported, scanner disabled, scan failed, or genuinely found nothing.
// The API now returns has_report and the latest report's scanner_status; this
// is the one place every surface (Assets, Endpoint Detail) turns them into a
// state. Nothing is inferred from a null category on its own.
import type { components } from '@/api/schema'

type Endpoint = components['schemas']['Endpoint']

// has_report / scanner_status are not in api/openapi.yaml yet (drift logged
// for Architect-EAMI), so they are typed here, not in the generated schema.
export type ScannerStatus = 'ok' | 'disabled' | 'error'
export type EndpointWithScannerStatus = Endpoint & ScannerStateSource

// Anything carrying the two fields: a /v1/endpoints row or detail, or a CMDB
// endpoint row (B-252 C3). One rule set for every surface.
export type ScannerStateSource = {
  has_report?: boolean | null
  scanner_status?: Record<string, string> | null
}

// The agent's scanner name for each category the UI shows.
export const CATEGORY_SCANNER = {
  ai_apps: 'ai_apps',
  local_models: 'models',
  mcp_servers: 'mcp_servers',
  cloud_clients: 'cloud_clients',
  gpus: 'gpu',
  network_activity: 'network_activity',
  python_envs: 'python_envs',
  node_projects: 'nodejs_ai',
} as const
export type Category = keyof typeof CATEGORY_SCANNER

export type CategoryState =
  | { kind: 'count'; count: number } // ran: a real count, 0 means checked and found nothing
  | { kind: 'never' }                // no scan report exists for this endpoint
  | { kind: 'disabled' }             // the scanner was off for the latest scan
  | { kind: 'error' }                // the scanner ran and failed in the latest scan
  | { kind: 'unknown' }              // the report predates scanner_status and shows nothing

export function categoryState(ep: ScannerStateSource, category: Category, count: number): CategoryState {
  if (ep.has_report === false) return { kind: 'never' }
  const status = ep.scanner_status?.[CATEGORY_SCANNER[category]]
  if (status === 'ok') return { kind: 'count', count }
  if (status === 'disabled') return { kind: 'disabled' }
  if (status === 'error') return { kind: 'error' }
  // No status (an older agent): items prove the scanner ran; an empty or
  // null category proves nothing, so it is "not known", never "0".
  return count > 0 ? { kind: 'count', count } : { kind: 'unknown' }
}

export const STATE_LABEL: Record<Exclude<CategoryState['kind'], 'count'>, string> = {
  never: 'Never reported',
  disabled: 'Disabled',
  error: 'Scan failed',
  unknown: 'Not known',
}

export const STATE_DESCRIPTION: Record<Exclude<CategoryState['kind'], 'count'>, string> = {
  never: 'No scan report has been received from this endpoint yet.',
  disabled: 'This scanner was turned off for this endpoint in its latest scan.',
  error: 'This scanner ran in the latest scan but failed, so there is no result.',
  unknown: "This endpoint's agent doesn't report scanner status, so an empty result can't be told apart from a disabled scanner.",
}
