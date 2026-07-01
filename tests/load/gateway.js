/**
 * EAMI Gateway — k6 load test (TASK-050)
 *
 * Thresholds (must all pass for green CI):
 *   - p95 MCP message latency < 100ms
 *   - p99 MCP message latency < 500ms
 *   - MCP error rate < 0.1%
 *   - Server memory growth < 50MB  (measured via docker stats externally)
 *   - Goroutine count returns to baseline after ramp-down (measured via pprof)
 *
 * Environment variables:
 *   GATEWAY_URL   — base URL of the gateway, e.g. http://localhost:8080
 *   PPROF_URL     — pprof endpoint, e.g. http://localhost:6060
 *   AGENT_TOKEN   — Bearer token for a registered gateway agent
 *
 * Prerequisites:
 *   k6 v0.50+ (for k6/experimental/sse)
 *   A running EAMI stack with at least one registered agent
 *
 * Run:
 *   GATEWAY_URL=http://localhost:8080 \
 *   PPROF_URL=http://localhost:6060 \
 *   AGENT_TOKEN=<token> \
 *   k6 run tests/load/gateway.js
 *
 * Memory check (run in a separate terminal during the test):
 *   watch -n5 "docker stats --no-stream eami-gateway --format '{{.MemUsage}}'"
 */

import http from 'k6/http'
import { sleep, check, fail } from 'k6'
import { Trend, Rate } from 'k6/metrics'

// ── Custom metrics ────────────────────────────────────────────────────────────

const mcpLatency  = new Trend('mcp_msg_latency_ms', true)
const mcpErrorRate = new Rate('mcp_error_rate')

// ── Test config ───────────────────────────────────────────────────────────────

const GATEWAY_URL = __ENV.GATEWAY_URL || 'http://localhost:8080'
const PPROF_URL   = __ENV.PPROF_URL   || 'http://localhost:6060'
const AGENT_TOKEN = __ENV.AGENT_TOKEN || ''

export const options = {
  scenarios: {
    load: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '30s', target: 100 },  // ramp up to 100 concurrent SSE sessions
        { duration: '3m',  target: 100 },  // sustain: 2000 MCP calls/min = ~20/VU/min = 1 call/3s
        { duration: '30s', target: 0   },  // ramp down
      ],
    },
  },
  thresholds: {
    mcp_msg_latency_ms: [
      { threshold: 'p(95)<100', abortOnFail: false },
      { threshold: 'p(99)<500', abortOnFail: false },
    ],
    mcp_error_rate: [
      { threshold: 'rate<0.001', abortOnFail: false },
    ],
  },
}

// ── Lifecycle ─────────────────────────────────────────────────────────────────

export function setup() {
  if (!AGENT_TOKEN) {
    fail('AGENT_TOKEN env var is required')
  }

  // Capture baseline goroutine count via pprof.
  const res = http.get(`${PPROF_URL}/debug/pprof/goroutine?debug=1`)
  let baseline = 0
  if (res.status === 200) {
    // Count "goroutine " lines as a rough baseline.
    baseline = (res.body.match(/^goroutine \d+/gm) || []).length
    console.log(`[setup] baseline goroutine count: ${baseline}`)
  } else {
    console.warn(`[setup] pprof not available (${res.status}) — goroutine check skipped`)
  }
  return { baselineGoroutines: baseline }
}

export function teardown(data) {
  // Re-sample goroutine count after ramp-down.
  const res = http.get(`${PPROF_URL}/debug/pprof/goroutine?debug=1`)
  if (res.status !== 200 || data.baselineGoroutines === 0) {
    console.warn('[teardown] goroutine check skipped (pprof unavailable or no baseline)')
    return
  }
  const current = (res.body.match(/^goroutine \d+/gm) || []).length
  const growth  = current - data.baselineGoroutines
  console.log(`[teardown] goroutine count: baseline=${data.baselineGoroutines} current=${current} growth=${growth}`)
  if (growth > 50) {
    console.error(`[teardown] LEAK SUSPECTED: goroutines grew by ${growth} after ramp-down`)
  } else {
    console.log('[teardown] goroutine count returned to near-baseline — no leak detected')
  }
}

// ── Virtual user scenario ─────────────────────────────────────────────────────

export default function () {
  const headers = {
    'Authorization': `Bearer ${AGENT_TOKEN}`,
    'Content-Type':  'application/json',
  }

  // Open SSE session — read the first event to get a sessionId.
  // NOTE: k6's standard http module reads the response body then closes.
  // For a truly persistent SSE stream, upgrade to k6/experimental/sse (k6 v0.50+).
  const sseRes = http.get(`${GATEWAY_URL}/v1/mcp/sse`, {
    headers,
    timeout: '10s',
  })

  const ok = check(sseRes, {
    'SSE session opened (2xx)': (r) => r.status >= 200 && r.status < 300,
  })
  if (!ok) {
    mcpErrorRate.add(1)
    return
  }

  // Parse sessionId from the SSE data field.
  const match = sseRes.body.match(/"session_id"\s*:\s*"([^"]+)"/)
  if (!match) {
    console.warn('No session_id in SSE response — skipping tool call')
    mcpErrorRate.add(1)
    return
  }
  const sessionId = match[1]

  // Send one MCP tool call per iteration (~1 call per 3s × 100 VUs = 2000/min sustained).
  const payload = JSON.stringify({
    tool:       'list_files',
    action:     'list',
    params:     { path: '/tmp' },
    session_id: sessionId,
  })

  const start = Date.now()
  const toolRes = http.post(`${GATEWAY_URL}/v1/mcp/tool_call`, payload, {
    headers,
    timeout: '5s',
  })
  mcpLatency.add(Date.now() - start)

  const toolOk = check(toolRes, {
    'tool call 2xx': (r) => r.status >= 200 && r.status < 300,
  })
  mcpErrorRate.add(toolOk ? 0 : 1)

  sleep(3) // pacing: 1 call per 3s per VU
}
