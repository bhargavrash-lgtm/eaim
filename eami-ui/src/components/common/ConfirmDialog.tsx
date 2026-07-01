import { type ReactNode } from 'react'

interface ConfirmDialogProps {
  title: string
  description?: string
  confirmLabel?: string
  cancelLabel?: string
  destructive?: boolean
  open?: boolean
  isLoading?: boolean
  onConfirm: () => void
  onCancel: () => void
  children?: ReactNode
}

export function ConfirmDialog({
  title,
  description,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  destructive = false,
  onConfirm,
  onCancel,
  children,
}: ConfirmDialogProps) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div className="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
        <h2 className="text-base font-semibold text-gray-900">{title}</h2>
        {description && <p className="mt-1 text-sm text-gray-500">{description}</p>}
        {children && <div className="mt-3">{children}</div>}
        <div className="mt-5 flex justify-end gap-3">
          <button
            onClick={onCancel}
            className="rounded border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50"
          >
            {cancelLabel}
          </button>
          <button
            onClick={onConfirm}
            className={`rounded px-4 py-2 text-sm font-medium text-white ${
              destructive
                ? 'bg-red-600 hover:bg-red-700'
                : 'bg-brand-600 hover:bg-brand-700'
            }`}
          >
            {confirmLabel}
          </button>
      
        </div>
      </div>
    </div>
  )
}
