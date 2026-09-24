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
  // Genuine extension (B-104's remaining-pages migration): DiscoverPage's
  // existing empty state has a real icon + description EmptyState itself
  // already supports -- emptyMessage alone (a plain string title) can't
  // reproduce that. Optional, backward compatible: every other consumer
  // keeps using the plain `emptyMessage` string with no change.
  renderEmpty?: () => ReactNode
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

// SortColumn -- one entry in the active multi-column sort ordering
// (B-220). Array order IS priority order: index 0 is the primary sort,
// later entries only break ties left by every entry before them.
interface SortColumn {
  key: string
  dir: 'asc' | 'desc'
}

export function DataTable<T extends Record<string, unknown>>({
  columns,
  data,
  loading,
  emptyMessage = 'No results',
  renderEmpty,
  onRowClick,
  pageSize = 25,
  getRowId,
  highlightRowId,
}: DataTableProps<T>) {
  // sortColumns replaces the old single sortKey/sortDir pair (B-220: real
  // multi-column sort, MATURITY_AUDIT.md Part A). A plain click keeps the
  // exact pre-existing single-column behavior (replace the whole ordering
  // with just this column, toggling direction if it was already the sole
  // active column) -- AC1's "no regression" requirement means this branch
  // must reproduce the old handleSort exactly, not just approximately.
  // Shift-click is additive: toggles this column's direction if it's
  // already in the ordering, otherwise appends it as the next tiebreaker
  // -- the standard Excel/Sheets convention, confirmed achievable within
  // this component's existing structure via the native MouseEvent's own
  // shiftKey (no new dependency, no structural change needed).
  const [sortColumns, setSortColumns] = useState<SortColumn[]>([])
  const [page, setPage] = useState(1)
  const highlightRef = useRef<HTMLTableRowElement | null>(null)

  function handleSort(key: string, additive: boolean) {
    setSortColumns((prev) => {
      const existingIdx = prev.findIndex((c) => c.key === key)
      if (!additive) {
        // Plain click: reproduces the old single-column toggle exactly --
        // if this was already the ONLY active column, flip its direction;
        // otherwise (nothing active, or a different/multi-column state),
        // replace everything with just this column ascending.
        if (prev.length === 1 && existingIdx === 0) {
          return [{ key, dir: prev[0].dir === 'asc' ? 'desc' : 'asc' }]
        }
        return [{ key, dir: 'asc' }]
      }
      // Shift-click: toggle in place if already active, else append as
      // the next-priority tiebreaker.
      if (existingIdx === -1) {
        return [...prev, { key, dir: 'asc' }]
      }
      const next = [...prev]
      next[existingIdx] = { key, dir: next[existingIdx].dir === 'asc' ? 'desc' : 'asc' }
      return next
    })
  }

  // Generic comparator, unchanged from the pre-existing single-column
  // logic (Part A.3 confirmed: Column<T> has no per-column comparator
  // field anywhere in this codebase, so there is nothing column-specific
  // to preserve beyond this) -- now applied in sortColumns' own priority
  // order, falling through to the next column only when the current one
  // reports a tie (cmp === 0).
  function compareByColumn(a: T, b: T, key: string): number {
    const ar = a as Record<string, unknown>
    const br = b as Record<string, unknown>
    const av = ar[key]
    const bv = br[key]
    return typeof av === 'string' && typeof bv === 'string'
      ? av.localeCompare(bv)
      : typeof av === 'number' && typeof bv === 'number'
      ? av - bv
      : 0
  }

  const sorted = sortColumns.length
    ? [...data].sort((a, b) => {
        for (const { key, dir } of sortColumns) {
          const cmp = compareByColumn(a, b, key)
          if (cmp !== 0) return dir === 'asc' ? cmp : -cmp
        }
        return 0
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
    // Deliberately depends on `data`/`pageSize`/`sortColumns`, not
    // `sorted`/`page` -- `sorted` is a fresh array every render derived
    // from `data`+`sortColumns` together (so depending on both of THOSE
    // covers it without an unstable-reference re-run every render) and
    // `page` is this effect's own setState target, so including either
    // directly would just be a same-value re-run every render.
    // `sortColumns` (code-review finding, B-220): omitted before this
    // fix, since single-column sort was never reachable together with a
    // real highlightRowId consumer -- now that AgentsPage.tsx combines
    // sortable columns with highlightRowId (B-092), a highlighted row's
    // page position can genuinely change when its sort key changes, and
    // without this dependency the auto-advance/scroll-into-view effects
    // below would silently go stale on the next sort click.
  }, [highlightRowId, getRowId, data, pageSize, sortColumns])

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
    return renderEmpty ? <>{renderEmpty()}</> : <EmptyState title={emptyMessage} />
  }

  return (
    <div className="overflow-hidden rounded-lg border border-gray-200">
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-gray-200 text-sm">
          <thead className="bg-gray-50">
            <tr>
              {columns.map((col) => {
                const sortIdx = sortColumns.findIndex((c) => c.key === col.key)
                const active = sortIdx !== -1
                // Priority badge (2nd+ active column only -- matches the
                // Excel/Sheets convention of not cluttering a single-
                // column sort with a redundant "1") so a user can see
                // WHICH tiebreaker order is in effect, not just that more
                // than one column is active.
                const showPriority = active && sortColumns.length > 1
                return (
                  <th
                    key={col.key}
                    scope="col"
                    title={col.sortable ? 'Click to sort · Shift-click to add as a secondary sort' : undefined}
                    className={`px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 ${
                      col.sortable ? 'cursor-pointer select-none hover:text-gray-700' : ''
                    } ${col.className ?? ''}`}
                    onClick={col.sortable ? (e) => handleSort(col.key, e.shiftKey) : undefined}
                  >
                    <span className="flex items-center gap-1">
                      {col.header}
                      {active ? (
                        <span className="flex items-center gap-0.5">
                          {sortColumns[sortIdx].dir === 'asc' ? (
                            <ChevronUp className="h-3 w-3" />
                          ) : (
                            <ChevronDown className="h-3 w-3" />
                          )}
                          {showPriority && (
                            <span className="text-2xs font-bold text-gray-400">{sortIdx + 1}</span>
                          )}
                        </span>
                      ) : null}
                    </span>
                  </th>
                )
              })}
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
