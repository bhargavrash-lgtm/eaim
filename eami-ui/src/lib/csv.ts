// CSV helpers deliberately neutralize spreadsheet formulas in text fields.
// A CSV is commonly opened in Excel or Sheets, where a value beginning with a
// formula sigil can otherwise execute as a formula merely by being opened.

function safeCell(value: unknown): string {
  const text = value == null ? '' : String(value)
  const trimmed = text.replace(/^[ \t\r\n]+/, '')
  const formulaSafe = /^[=+\-@]/.test(trimmed) ? `'${text}` : text
  return `"${formulaSafe.replace(/"/g, '""')}"`
}

export function buildCSV(headers: string[], rows: unknown[][]): string {
  return [headers, ...rows].map((row) => row.map(safeCell).join(',')).join('\r\n') + '\r\n'
}

export function downloadCSV(filename: string, csv: string | Blob): void {
  const blob = typeof csv === 'string' ? new Blob([csv], { type: 'text/csv;charset=utf-8' }) : csv
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  link.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 0)
}
