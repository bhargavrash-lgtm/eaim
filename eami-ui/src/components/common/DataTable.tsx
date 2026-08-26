import { type ReactNode, useEffect, useRef, useState } from 'react'
import { ChevronUp, ChevronDown } from 'lucide-react'
import { LoadingSpinner } from './LoadingSpinner'
import { EmptyState } from './EmptyState'

export interface Column<T> {
  key: string
  header: string
  render?: (row: T) => ReactNode
  sortable?: boolean
  className?: string
}

interface DataTableProps<T extends Record<string, unknown>> {
  columns: Column<T>[]
  data: T[]
  loading?: boolean
  emptyMessage?: string
  onRowClick?: (row: T) => void
  pageSize?: number
  // Deep-linking/highlighting by ID (B-092) -- getRowId identifies each
  // row (usually `(row) => row.id`); highlightRowId is typically read
  // from a `?highlight=<id>` URL param by the caller. When the matching
  // row isn't on the currently-sorted-and-paged slice, the table
  // auto-advances to whichever page contains it, so a deep link always
  // lands on a visible, highlighted row rather than silently doing
  // nothing on page 1.
  getRowId?: (row: T) => string
  highlightRowId?: string | null
}

export function DataTable<T extends Record<string, unknown>>({
  columns,
  data,
  loading,
  emptyMessage = 'No results',
  onRowClick,
  pageSize = 25,
  getRowId,
  highlightRowId,
}: DataTableProps<T>) {
  const [sortKey, setSortKey] = useState<string | null>(null)
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('asc')
  const [page, setPage] = useState(1)
  const highlightRef = useRef<HTMLTableRowElement | null>(null)

  function handleSort(key: string) {
    if (sortKey === key) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  const sorted = sortKey
    ? [...data].sort((a, b) => {
        const ar = a as Record<string, unknown>
        const br = b as Record<string, unknown>
        const av = ar[sortKey]
        const bv = br[sortKey]
        const cmp =
          typeof av === 'string' && typeof bv === 'string'
            ? av.localeCompare(bv)
            : typeof av === 'number' && typeof bv === 'number'
            ? av - bv
            : 0
        return sortDir === 'asc' ? cmp : -cmp
      })
    : data

  const totalPages = Math.max(1, Math.ceil(sorted.length / pageSize))
  const paged = sorted.slice((page - 1) * pageSize, page * pageSize)

  // Auto-advance to whichever page actually contains the highlighted row
  // -- without this, a deep link to a row past page 1 would silently show
  // page 1 with nothing highlighted, indistinguishable from "not found".
  useEffect(() => {
    if (!highlightRowId || !getRowId) return
    const idx = sorted.findIndex((row) => getRowId(row) === highlightRowId)
    if (idx === -1) return
    const targetPage = Math.floor(idx / pageSize) + 1
    if (targetPage !== page) setPage(targetPage)
    // Deliberately depends on `data`/`pageSize`, not `sorted`/`page` --
    // `sorted` is a fresh array every render (sort is unaffected by which
    // page we're on) and `page` is this effect's own setState target, so
    // including either would just be a same-value re-run every render.
  }, [highlightRowId, getRowId, data, pageSize])

  // Scroll the highlighted row into view once it's actually rendered
  // (i.e. after the page-advance effect above, if one was needed).
  useEffect(() => {
    if (highlightRowId && highlightRef.current) {
      highlightRef.current.scrollIntoView({ behavior: 'smooth', block: 'center' })
    }
  }, [highlightRowId, page])

  if (loading) {
    return (
      <div className="flex items-center justify-center py-16">
        <LoadingSpinner />
      </div>
    )
  }

  if (data.length === 0) {
    return <EmptyState title={emptyMessage} />
  }

  return (
    <div className="overflow-hidden rounded-lg border border-gray-200">
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-gray-200 text-sm">
          <thead className="bg-gray-50">
            <tr>
              {columns.map((col) => (
                <th
                  key={col.key}
                  scope="col"
                  className={`px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 ${
                    col.sortable ? 'cursor-pointer select-none hover:text-gray-700' : ''
                  } ${col.className ?? ''}`}
                  onClick={col.sortable ? () => handleSort(col.key) : undefined}
                >
                  <span className="flex items-center gap-1">
                    {col.header}
                    {col.sortable && sortKey === col.key ? (
                      sortDir === 'asc' ? (
                        <ChevronUp className="h-3 w-3" />
                      ) : (
                        <ChevronDown className="h-3 w-3" />
                      )
                    ) : null}
                  </span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 bg-white">
            {paged.map((row, idx) => {
              const rowId = getRowId?.(row)
              const isHighlighted = rowId != null && rowId === highlightRowId
              return (
                <tr
                  key={rowId ?? idx}
                  id={rowId ? `row-${rowId}` : undefined}
                  ref={isHighlighted ? highlightRef : undefined}
                  onClick={onRowClick ? () => onRowClick(row) : undefined}
                  className={`${onRowClick ? 'cursor-pointer hover:bg-gray-50' : ''} ${
                    isHighlighted ? 'bg-amber-50 ring-2 ring-inset ring-amber-400' : ''
                  }`}
                >
                  {columns.map((col) => (
                    <td key={col.key} className={`whitespace-nowrap px-4 py-3 text-gray-700 ${col.className ?? ''}`}>
                      {col.render ? col.render(row) : String((row as Record<string, unknown>)[col.key] ?? '')}
                    </td>
                  ))}
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      {totalPages > 1 && (
        <div className="flex items-center justify-between border-t border-gray-200 bg-gray-50 px-4 py-3">
          <span className="text-xs text-gray-500">
            Page {page} of {totalPages}
          </span>
          <div className="flex gap-2">
            <button
              disabled={page === 1}
              onClick={() => setPage((p) => p - 1)}
              className="rounded border border-gray-300 px-2 py-1 text-xs disabled:opacity-40"
            >
              Previous
            </button>
            <button
              disabled={page === totalPages}
              onClick={() => setPage((p) => p + 1)}
              className="rounded border border-gray-300 px-2 py-1 text-xs disabled:opacity-40"
            >
              Next
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
