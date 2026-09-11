import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from 'react'

interface ToastState {
  id: number
  message: string
  type?: 'success' | 'error'
}

interface ToastOptions {
  type?: 'success' | 'error'
  durationMs?: number
}

interface ToastContextValue {
  showToast: (message: string, options?: ToastOptions) => void
}

const ToastContext = createContext<ToastContextValue | null>(null)

// Single shared toast pattern for the whole app -- replaces the two
// independently hand-rolled `Toast` implementations this consolidates
// (AlertsPage.tsx's floating dismiss-able notification, SettingsPage.tsx's
// inline success/error message). One toast visible at a time: a second
// call while one is showing replaces it outright rather than queuing --
// matches how every existing call site already behaved (each owned a
// single `toast` state slot, so a second trigger always overwrote the
// first anyway; queuing would be new behavior nobody asked for).
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<ToastState | null>(null)
  const timeoutRef = useRef<number | null>(null)
  const idRef = useRef(0)

  const showToast = useCallback((message: string, options?: ToastOptions) => {
    if (timeoutRef.current != null) window.clearTimeout(timeoutRef.current)
    const id = ++idRef.current
    setToast({ id, message, type: options?.type })
    const durationMs = options?.durationMs ?? 4000
    timeoutRef.current = window.setTimeout(() => {
      setToast((current) => (current?.id === id ? null : current))
    }, durationMs)
  }, [])

  function dismiss() {
    if (timeoutRef.current != null) window.clearTimeout(timeoutRef.current)
    setToast(null)
  }

  return (
    <ToastContext.Provider value={{ showToast }}>
      {children}
      {toast && <ToastHost message={toast.message} type={toast.type} onDismiss={dismiss} />}
    </ToastContext.Provider>
  )
}

export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast() must be used within a <ToastProvider>')
  return ctx
}

const TYPE_STYLES: Record<'success' | 'error', string> = {
  success: 'bg-green-50 text-green-800 border border-green-200',
  error: 'bg-red-50 text-red-800 border border-red-200',
}

// No `type` -- AlertsPage's original neutral dark floating notification.
// `type: 'success' | 'error'` -- SettingsPage's original green/red
// inline message, now floating like every other toast in the app (an
// explicit, approved position change: one shared app-level toast host
// instead of each page managing its own in-flow placement).
function ToastHost({ message, type, onDismiss }: { message: string; type?: 'success' | 'error'; onDismiss: () => void }) {
  const typeClass = type ? TYPE_STYLES[type] : 'bg-gray-900 text-white'
  return (
    <div
      className={`fixed bottom-6 right-6 z-[70] flex max-w-sm items-center gap-3 rounded-lg px-4 py-3 text-sm font-medium shadow-lg ${typeClass}`}
    >
      <span className="flex-1">{message}</span>
      <button
        onClick={onDismiss}
        className={`shrink-0 transition-colors ${type ? 'opacity-60 hover:opacity-100' : 'text-gray-400 hover:text-white'}`}
        aria-label="Dismiss"
      >
        ×
      </button>
    </div>
  )
}
