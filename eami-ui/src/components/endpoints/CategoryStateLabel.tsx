// Renders a detection category's honest state (see scannerState.ts): the real
// count when the scanner ran, otherwise a muted label ("Never reported",
// "Disabled", "Scan failed", "Not known"). Never a bare 0 for a scanner that
// didn't run. The visible label is the accessible name; the reason is
// screen-reader text plus a tooltip, so a button containing this label isn't
// named by a whole sentence.
import { STATE_DESCRIPTION, STATE_LABEL, type CategoryState } from './scannerState'

export function CategoryStateLabel({ state, countClassName = '' }: { state: CategoryState; countClassName?: string }) {
  if (state.kind === 'count') return <span className={countClassName}>{state.count}</span>
  return (
    <span
      className="whitespace-nowrap text-xs italic text-gray-500"
      title={STATE_DESCRIPTION[state.kind]}
      data-scanner-state={state.kind}
    >
      {STATE_LABEL[state.kind]}
      <span className="sr-only">: {STATE_DESCRIPTION[state.kind]}</span>
    </span>
  )
}
