// AssetClassificationTab -- a detail page's Classification tab (DESIGN_SYSTEM.md
// §7.7): the same classification logic Assets uses (AssetClassificationForm),
// over one asset's own CMDB row. Admins can change it; operators and viewers
// see it read-only; approvers never get this tab (CMDB reads exclude them).
// First built for Agent Detail (B-252 C1); generalised by kind for Endpoint
// Detail (C2) rather than duplicated.
import { Button, EmptyState, LoadingSpinner } from '@/components/common'
import { AssetClassificationForm } from '@/components/cmdb/AssetClassificationForm'
import { type CMDBAssetKind, useCMDBAsset, useCMDBClassifications } from '@/hooks/useCMDB'
import { can, useOrgRole } from '@/lib/rbac'

const CARD = 'rounded-[10px] border border-[rgba(228,231,240,0.55)] bg-white shadow-l1'

export function AssetClassificationTab({ kind, id, noun }: { kind: CMDBAssetKind; id: string; noun: string }) {
  const role = useOrgRole()
  const classifications = useCMDBClassifications()
  const asset = useCMDBAsset(kind, id)

  if (classifications.isLoading || asset.isLoading) {
    return <div className={`${CARD} flex h-[220px] items-center justify-center`}><LoadingSpinner /></div>
  }
  if (classifications.isError || asset.isError) {
    return (
      <div className={`${CARD} flex h-[220px] flex-col items-center justify-center gap-3`}>
        <EmptyState title="Failed to load classification" description={`This ${noun}'s CMDB classification could not be loaded.`} />
        <Button variant="outline" size="sm" isLoading={classifications.isFetching || asset.isFetching}
          onClick={() => { classifications.refetch(); asset.refetch() }}>Retry</Button>
      </div>
    )
  }
  if (!asset.data) {
    return (
      <div className={`${CARD} flex h-[220px] items-center justify-center`}>
        <EmptyState title="No CMDB record" description={`This ${noun} has no CMDB classification record.`} />
      </div>
    )
  }
  return (
    <div className={`${CARD} flex flex-col`}>
      {/* key: re-seed the form's picker from the refetched row after a save */}
      <AssetClassificationForm
        key={`${asset.data.classification.type_id}-${asset.data.classification.classification_source}`}
        asset={asset.data}
        categories={classifications.data?.data ?? []}
        canEdit={can.classifyAsset(role)}
      />
    </div>
  )
}
