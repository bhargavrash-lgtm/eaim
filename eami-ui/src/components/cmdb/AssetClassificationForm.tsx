// AssetClassificationForm -- the per-asset classification logic, extracted
// from AssetsPage's AssetClassificationPanel (B-252 C1) so Assets' panel and
// Agent Detail's Classification tab share one implementation. Behaviour is
// unchanged: the resolved classification, and for admins a type picker
// limited to the asset's kind ("Use default" resets), saved via
// PATCH /v1/cmdb/assets/{kind}/{id}/classification (admin-only server-side).
import { useMemo, useState } from 'react'
import { X } from 'lucide-react'
import { Button, useToast } from '@/components/common'
import { type CMDBAsset, type CMDBAssetKind, type CMDBCategory, useSetCMDBAssetClassification } from '@/hooks/useCMDB'

export const KIND_LABEL: Record<CMDBAssetKind, string> = { endpoint: 'Endpoint', agent: 'Agent', tool: 'Tool' }

export function cmdbErrorMessage(error: unknown) {
  return error && typeof error === 'object' && 'message' in error && typeof error.message === 'string' ? error.message : 'The request could not be completed.'
}

export function AssetClassificationForm({ asset, categories, canEdit, onDone, onCancel, header, note }: {
  asset: CMDBAsset
  categories: CMDBCategory[]
  canEdit: boolean
  // The slide-over panel's own header (title + close), rendered here so the
  // close button is disabled while a save is pending, as before extraction.
  header?: { onClose: () => void }
  // An optional one-line note under the header (B-252 C1's transition note).
  note?: string
  // Called after a successful save (the panel closes; the tab stays).
  onDone?: () => void
  // When set, a Cancel button is shown (the panel's footer).
  onCancel?: () => void
}) {
  const { showToast } = useToast(); const mutation = useSetCMDBAssetClassification(); const [value, setValue] = useState(asset.classification.classification_source === 'explicit' ? asset.classification.type_id : '')
  const types = useMemo(() => categories.flatMap((c) => c.types).filter((t) => t.asset_kind === asset.asset_kind), [categories, asset.asset_kind])
  async function save() { try { await mutation.mutateAsync({ kind: asset.asset_kind, id: asset.id, ciTypeId: value || null }); showToast(value ? 'Asset classification updated.' : 'Asset reset to its default classification.', { type: 'success' }); onDone?.() } catch (e) { showToast(cmdbErrorMessage(e), { type: 'error' }) } }
  return <>
    {header && <div className="flex items-center justify-between border-b border-gray-200 px-6 py-4"><div><h2 className="font-semibold text-ink">{asset.name}</h2><p className="text-xs text-gray-500">{KIND_LABEL[asset.asset_kind]} classification</p></div><button disabled={mutation.isPending} onClick={header.onClose}><X className="h-5 w-5 text-gray-400" /></button></div>}
    {note && <p className="border-b border-gray-200 bg-gray-50 px-6 py-2 text-xs text-gray-600">{note}</p>}
    <div className="flex-1 space-y-5 overflow-y-auto p-6"><div className="rounded-md border border-gray-200 p-4"><div className="text-xs uppercase tracking-wide text-gray-400">Resolved classification</div><div className="mt-1 font-medium text-gray-800">{asset.classification.category_name} / {asset.classification.type_name}</div><div className="mt-1 text-xs text-gray-500">Source: {asset.classification.classification_source}</div></div>{canEdit ? <label className="block text-sm text-gray-700">Type<select value={value} onChange={(e) => setValue(e.target.value)} className="mt-2 w-full rounded-md border border-gray-300 px-3 py-2"><option value="">Use {KIND_LABEL[asset.asset_kind]} default</option>{types.map((t) => <option key={t.id} value={t.id}>{categories.find((c) => c.id === t.category_id)?.name} / {t.name}{t.is_default ? ' (default)' : ''}</option>)}</select></label> : <p className="text-sm text-gray-500">Your role has read-only access to CMDB classifications.</p>}</div>
    {(onCancel || canEdit) && <div className="flex justify-end gap-2 border-t border-gray-200 px-6 py-4">{onCancel && <Button variant="secondary" disabled={mutation.isPending} onClick={onCancel}>Cancel</Button>}{canEdit && <Button isLoading={mutation.isPending} onClick={save}>Save classification</Button>}</div>}
  </>
}
