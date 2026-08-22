import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import { setupAuthRefresh } from './lib/auth'
import { branding } from './branding/config'
import './index.css'

setupAuthRefresh()

// Tab title's product name comes from branding/config.ts at runtime (not
// just the static index.html fallback) so changing displayName there
// takes effect without a rebuild -- the "-- Enterprise AI Governance"
// descriptor is unrelated to branding and stays a literal here, matching
// index.html's existing copy.
document.title = `${branding.displayName} — Enterprise AI Governance`

const rootEl = document.getElementById('root')
if (!rootEl) throw new Error('Root element #root not found')

createRoot(rootEl).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
