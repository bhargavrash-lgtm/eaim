import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { ChevronRight, FolderTree, Monitor, Search, Settings2, X } from 'lucide-react'
import { AppTopBar } from '@/components/layout/AppTopBar'
import { AssetWorkspaceBadge } from '@/components/cmdb/AssetWorkspaceBadge'
import { AssetClassificationForm, KIND_LABEL, cmdbErrorMessage as errorMessage } from '@/components/cmdb/AssetClassificationForm'
import { Button, DataTable, EmptyState, SlideOverPanel, useToast } from '@/components/common'
import type { Column } from '@/components/common/DataTable'
import { PageHeader } from '@/components/common/PageHeader'
import { RiskPill } from '@/components/common/RiskPill'
import { StatusPill } from '@/components/common/StatusPill'
import {
  type CMDBAsset, type CMDBAssetKind, type CMDBCategory, type CMDBEndpointOS, type CMDBType,
  useCMDBAssets, useCMDBClassifications, useCreateCMDBCategory, useCreateCMDBType,
  useDeleteCMDBCategory, useDeleteCMDBType,
  useUpdateCMDBCategory, useUpdateCMDBType, useCMDBWorkspaces,
} from '@/hooks/useCMDB'
import { useAuthStore } from '@/stores/authStore'
import { CategoryStateLabel } from '@/components/endpoints/CategoryStateLabel'
import { categoryState } from '@/components/endpoints/scannerState'
import { formatOS, formatRelativeTime } from '@/components/endpoints/format'

const KINDS: CMDBAssetKind[] = ['endpoint', 'agent', 'tool']
const OS_VALUES: CMDBEndpointOS[] = ['windows', 'linux', 'darwin']


export function AssetsPage() {
  const isAdmin = useAuthStore((s) => s.user?.role === 'admin')
  const navigate = useNavigate()
  // Every filter and the page live in the URL (B-252 C4): /discover redirects
  // to /assets?kind=endpoint, a view is linkable and survives reload, and Back
  // from a detail page restores exactly the list it was opened from.
  const [searchParams, setSearchParams] = useSearchParams()
  const kindParam = searchParams.get('kind'); const osParam = searchParams.get('os')
  const kind = KINDS.includes(kindParam as CMDBAssetKind) ? (kindParam as CMDBAssetKind) : undefined
  const os = kind === 'endpoint' && OS_VALUES.includes(osParam as CMDBEndpointOS) ? (osParam as CMDBEndpointOS) : undefined
  const categoryId = searchParams.get('category_id') || undefined
  const typeId = searchParams.get('type_id') || undefined
  const workspaceId = searchParams.get('workspace_id') || undefined
  const query = searchParams.get('q') ?? ''
  const page = Math.max(1, Number.parseInt(searchParams.get('page') ?? '1', 10) || 1)
  // One writer for the URL state. Changing any filter returns to page 1.
  function updateParams(next: Partial<Record<'kind' | 'os' | 'category_id' | 'type_id' | 'workspace_id' | 'q', string | undefined>>, nextPage?: number) {
    const p = new URLSearchParams(searchParams)
    for (const [k, v] of Object.entries(next)) { if (v) p.set(k, v); else p.delete(k) }
    if (p.get('kind') !== 'endpoint') p.delete('os')
    if (nextPage && nextPage > 1) p.set('page', String(nextPage)); else p.delete('page')
    setSearchParams(p, { replace: true })
  }
  const setOS = (next: CMDBEndpointOS | undefined) => updateParams({ os: next })
  const setPage = (next: number) => updateParams({}, next)
  // Search types locally and reaches the URL (and the API) after 250 ms, as Discover's did.
  const [search, setSearch] = useState(query)
  useEffect(() => setSearch(query), [query])
  useEffect(() => {
    if (search === query) return
    const t = setTimeout(() => updateParams({ q: search || undefined }), 250)
    return () => clearTimeout(t)
    // updateParams reads the latest searchParams on each render; only search drives this.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [search])
  const [manageOpen, setManageOpen] = useState(false); const [selected, setSelected] = useState<CMDBAsset | null>(null)
  const filters = { page, per_page: 25, kind, os, category_id: categoryId, type_id: typeId, workspace_id: workspaceId, q: query || undefined }
  const classifications = useCMDBClassifications(); const assets = useCMDBAssets(filters)
  const workspaces = useCMDBWorkspaces()
  const categories = classifications.data?.data ?? []; const rows = assets.data?.data ?? []; const total = assets.data?.meta.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / 25)); const filtered = Boolean(kind || os || categoryId || typeId || workspaceId || query)
  // counts ignore the sidebar's own category/type/kind selection (N1), so they stay usable for navigation
  const filteredCountByType = new Map((assets.data?.counts ?? []).map((count) => [count.type_id, count.count]))
  const navigationTotal = (assets.data?.counts ?? []).reduce((sum, count) => sum + count.count, 0)
  const selectedCategory = categories.find((category) => category.id === categoryId)
  const selectedType = selectedCategory?.types.find((type) => type.id === typeId)
  const heading = selectedType?.name ?? selectedCategory?.name ?? (kind ? `${KIND_LABEL[kind]}s` : 'Assets')
  const breadcrumb = heading === 'Assets' ? [{ label: 'Assets' }] : [{ label: 'Assets', href: '/assets' }, { label: heading }]
  const scopedSubtitle = selectedType
    ? `${selectedType.asset_kind} assets classified as ${selectedCategory?.name} / ${selectedType.name}`
    : selectedCategory ? `Assets classified under ${selectedCategory.name}` : 'Organization CMDB inventory with governed classification for endpoints, agents, and tools'
  // No Discovery license: the Endpoints view shows the honest pending state
  // (DESIGN_SYSTEM.md section 7.4), never a red error implying something broke.
  const endpointsUnlicensed = kind === 'endpoint' && classifications.data?.endpoint_inventory_available === false

  const nameColumn: Column<CMDBAsset> = { key: 'name', header: 'Name', render: (r) => <span className="font-medium text-ink">{r.name}</span> }
  // D3: the Endpoints view gets Discover's fleet columns (B-252 C3), each count
  // with its honest state (item 4); the mixed list below is unchanged.
  const count = (r: CMDBAsset, cat: 'ai_apps' | 'local_models' | 'mcp_servers' | 'gpus', n: number | null | undefined) =>
    <CategoryStateLabel countClassName="text-gray-600" state={categoryState(r, cat, n ?? 0)} />
  const endpointColumns: Column<CMDBAsset>[] = [
    nameColumn,
    { key: 'os', header: 'OS', render: (r) => <span className="text-gray-500">{formatOS(r.os)}</span> },
    // An endpoint row's detail is its agent version (cmdbAssetUnion).
    { key: 'detail', header: 'Agent version', render: (r) => <span className="font-mono text-xs text-gray-500">{r.detail || '—'}</span> },
    { key: 'last_seen', header: 'Last seen', render: (r) => <span className="text-gray-400">{r.last_seen ? formatRelativeTime(r.last_seen) : '—'}</span> },
    { key: 'ai_app_count', header: 'AI apps', render: (r) => count(r, 'ai_apps', r.ai_app_count) },
    { key: 'local_model_count', header: 'Local models', render: (r) => count(r, 'local_models', r.local_model_count) },
    { key: 'mcp_server_count', header: 'MCPs', render: (r) => count(r, 'mcp_servers', r.mcp_server_count) },
    { key: 'gpu_count', header: 'GPUs', render: (r) => count(r, 'gpus', r.gpu_count) },
    { key: 'classification', header: 'Classification', render: (r) => <div><div className="text-sm text-gray-700">{r.classification.type_name}</div><div className="text-xs text-gray-400">{r.classification.category_name} · {r.classification.classification_source}</div></div> },
    { key: 'risk_tier', header: 'Risk', render: (r) => r.risk_tier ? <RiskPill tier={r.risk_tier as 'low' | 'medium' | 'high' | 'critical'} /> : <span className="text-gray-400">—</span> },
    { key: 'workspace_label', header: 'Workspace', render: (r) => <AssetWorkspaceBadge scoped={r.workspace_label !== 'not_workspace_scoped'} workspaceName={r.workspace_name} /> },
  ]
  const mixedColumns: Column<CMDBAsset>[] = [
    nameColumn,
    { key: 'asset_kind', header: 'Kind', render: (r) => <span className="rounded bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-600">{KIND_LABEL[r.asset_kind]}</span> },
    { key: 'classification', header: 'Classification', render: (r) => <div><div className="text-sm text-gray-700">{r.classification.type_name}</div><div className="text-xs text-gray-400">{r.classification.category_name} · {r.classification.classification_source}</div></div> },
    { key: 'status', header: 'Status', render: (r) => r.asset_kind === 'endpoint' ? <span className="capitalize text-gray-500">{r.status}</span> : <StatusPill status={r.status as 'active' | 'suspended' | 'revoked' | 'connected' | 'degraded' | 'disconnected'} /> },
    { key: 'risk_tier', header: 'Risk', render: (r) => r.risk_tier ? <RiskPill tier={r.risk_tier as 'low' | 'medium' | 'high' | 'critical'} /> : <span className="text-gray-400">—</span> },
    { key: 'workspace_label', header: 'Workspace', render: (r) => <AssetWorkspaceBadge scoped={r.workspace_label !== 'not_workspace_scoped'} workspaceName={r.workspace_name} /> },
  ]
  const columns = kind === 'endpoint' ? endpointColumns : mixedColumns
  // Agent rows open Agent Detail (C1) and endpoint rows Endpoint Detail (C2).
  // Tool rows keep the classification panel until Tool Detail (C5); it says so.
  function openAsset(r: CMDBAsset) {
    if (r.asset_kind === 'agent') navigate(`/assets/agents/${r.id}`)
    else if (r.asset_kind === 'endpoint') navigate(`/assets/endpoints/${r.id}`)
    else setSelected(r)
  }
  function clearFilters() { setSearch(''); setSearchParams(new URLSearchParams(), { replace: true }) }

  return <div className="flex h-full flex-col">
    <AppTopBar breadcrumb={breadcrumb} action={isAdmin ? <Button size="sm" variant="outline" onClick={() => setManageOpen(true)}><Settings2 className="h-4 w-4" />Manage</Button> : undefined} />
    <PageHeader subtitle={scopedSubtitle} />
    <div className="flex min-h-0 flex-1 bg-gray-50">
      <aside className="w-72 shrink-0 overflow-y-auto border-r border-gray-200 bg-white p-4">
        <div className="mb-3 flex items-center justify-between"><div className="flex items-center gap-2 text-sm font-semibold text-ink"><FolderTree className="h-4 w-4" />Classifications</div>{filtered && <button className="text-xs text-brand-700 hover:underline" onClick={clearFilters}>Clear</button>}</div>
        <button onClick={() => updateParams({ kind: undefined, category_id: undefined, type_id: undefined })} className={`mb-2 flex w-full items-center justify-between rounded-md px-3 py-2 text-left text-sm ${!kind && !categoryId && !typeId ? 'bg-brand-50 font-medium text-brand-700' : 'text-gray-600 hover:bg-gray-50'}`}><span>All assets</span><span>{navigationTotal}</span></button>
        <div className="space-y-1">{categories.map((category) => <div key={category.id}>
          <button onClick={() => updateParams({ kind: undefined, category_id: category.id, type_id: undefined })} className={`flex w-full items-center justify-between rounded-md px-3 py-2 text-left text-sm ${categoryId === category.id && !typeId ? 'bg-brand-50 font-medium text-brand-700' : 'text-gray-700 hover:bg-gray-50'}`}><span className="flex items-center gap-1.5"><ChevronRight className="h-3.5 w-3.5" />{category.name}</span><span className="text-xs text-gray-400">{category.types.reduce((sum, type) => sum + (filteredCountByType.get(type.id) ?? 0), 0)}</span></button>
          <div className="ml-5 border-l border-gray-200 pl-2">{category.types.map((type) => <button key={type.id} onClick={() => updateParams({ category_id: category.id, type_id: type.id, kind: type.asset_kind })} className={`flex w-full items-center justify-between rounded px-2 py-1.5 text-left text-xs ${typeId === type.id ? 'bg-brand-50 font-medium text-brand-700' : 'text-gray-500 hover:bg-gray-50'}`}><span>{type.name}{type.is_default ? ' · default' : ''}</span><span>{filteredCountByType.get(type.id) ?? 0}</span></button>)}</div>
        </div>)}</div>
      </aside>
      <main className="min-w-0 flex-1 overflow-y-auto p-6">
        {!classifications.data?.endpoint_inventory_available && <div className="mb-4 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800">Endpoint inventory requires a Discovery license. Governed agents and tools remain visible.</div>}
        <div className="mb-4 flex flex-wrap items-center gap-3"><div className="relative min-w-64 flex-1"><Search className="absolute left-3 top-2.5 h-4 w-4 text-gray-400" /><input value={search} maxLength={200} onChange={(e) => setSearch(e.target.value)} aria-label="Search assets" placeholder="Search this classification" className="w-full rounded-md border border-gray-300 bg-white py-2 pl-9 pr-9 text-sm outline-none focus:border-brand-500" />{search && <button onClick={() => setSearch('')} className="absolute right-3 top-2.5 text-gray-400"><X className="h-4 w-4" /></button>}</div><select value={kind ?? ''} onChange={(e) => { const next = e.target.value as CMDBAssetKind | ''; updateParams({ kind: next || undefined, category_id: undefined, type_id: undefined }) }} className="rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-gray-700"><option value="">All kinds</option><option value="endpoint">Endpoints</option><option value="agent">Agents</option><option value="tool">Tools</option></select>{kind === 'endpoint' && <select aria-label="Platform" value={os ?? ''} onChange={(e) => setOS((e.target.value || undefined) as CMDBEndpointOS | undefined)} className="rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-gray-700"><option value="">All platforms</option>{OS_VALUES.map((v) => <option key={v} value={v}>{formatOS(v)}</option>)}</select>}<select value={workspaceId ?? ''} onChange={(e) => updateParams({ workspace_id: e.target.value || undefined })} className="rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-gray-700"><option value="">All workspaces</option>{(workspaces.data?.data ?? []).map((workspace) => <option key={workspace.id} value={workspace.id}>{workspace.name}</option>)}</select><span className="text-sm text-gray-500">{total} assets</span></div>
        {endpointsUnlicensed ? <div className="flex flex-col items-center justify-center gap-1.5 rounded-lg border-[1.5px] border-dashed border-gray-300 bg-white px-6 py-14 text-center shadow-l1"><Monitor className="h-8 w-8 text-gray-300" /><p className="text-sm font-medium text-gray-600">Endpoint inventory not available</p><p className="max-w-md text-xs text-gray-500">Endpoints require a Discovery license for your organization. Governed agents and tools remain available under All kinds.</p></div> : assets.isError ? <div className="rounded-lg border border-red-200 bg-white p-8 text-center"><p className="mb-3 text-sm text-red-700">{errorMessage(assets.error)}</p><Button variant="outline" isLoading={assets.isFetching} onClick={() => assets.refetch()}>Retry</Button></div> : <DataTable columns={columns} data={rows} loading={assets.isLoading} pageSize={25} getRowId={(r) => `${r.asset_kind}-${r.id}`} onRowClick={openAsset} renderEmpty={() => <EmptyState title={filtered ? 'No assets match these filters' : 'No CMDB assets yet'} description={filtered ? 'Clear or change the current classification and search filters.' : 'Discovered endpoints and configured agents or tools will appear here.'} />} />}
        {!endpointsUnlicensed && !assets.isError && totalPages > 1 && <div className="mt-4 flex items-center justify-between text-sm text-gray-500"><span>Page {page} of {totalPages}</span><div className="flex gap-2"><Button size="sm" variant="outline" disabled={page === 1} onClick={() => setPage(page - 1)}>Previous</Button><Button size="sm" variant="outline" disabled={page === totalPages} onClick={() => setPage(page + 1)}>Next</Button></div></div>}
      </main>
    </div>
    {manageOpen && <ManageClassificationsPanel categories={categories} onClose={() => setManageOpen(false)} />}
    {selected && <AssetClassificationPanel asset={selected} categories={categories} canEdit={isAdmin} onClose={() => setSelected(null)} />}
  </div>
}

function ManageClassificationsPanel({ categories, onClose }: { categories: CMDBCategory[]; onClose: () => void }) {
  const { showToast } = useToast(); const createCategory = useCreateCMDBCategory(); const updateCategory = useUpdateCMDBCategory(); const deleteCategory = useDeleteCMDBCategory()
  const createType = useCreateCMDBType(); const updateType = useUpdateCMDBType(); const deleteType = useDeleteCMDBType()
  const [editingCategory, setEditingCategory] = useState<CMDBCategory | null>(null); const [categoryName, setCategoryName] = useState(''); const [categoryDescription, setCategoryDescription] = useState('')
  const [editingType, setEditingType] = useState<CMDBType | null>(null); const [typeName, setTypeName] = useState(''); const [typeDescription, setTypeDescription] = useState(''); const [typeCategory, setTypeCategory] = useState(categories[0]?.id ?? ''); const [typeKind, setTypeKind] = useState<CMDBAssetKind>('endpoint'); const [makeDefault, setMakeDefault] = useState(false)
  const pending = createCategory.isPending || updateCategory.isPending || deleteCategory.isPending || createType.isPending || updateType.isPending || deleteType.isPending
  useEffect(() => { if (!typeCategory && categories[0]) setTypeCategory(categories[0].id) }, [categories, typeCategory])
  function fail(error: unknown) { showToast(errorMessage(error), { type: 'error' }) }
  async function saveCategory() { try { const body = { name: categoryName, description: categoryDescription || null, sort_order: editingCategory?.sort_order ?? categories.length * 10 + 10 }; if (editingCategory) await updateCategory.mutateAsync({ id: editingCategory.id, body }); else await createCategory.mutateAsync(body); showToast(editingCategory ? 'Category updated.' : 'Category created.', { type: 'success' }); setEditingCategory(null); setCategoryName(''); setCategoryDescription('') } catch (e) { fail(e) } }
  async function saveType() { try { const body = { category_id: typeCategory, asset_kind: typeKind, name: typeName, description: typeDescription || null, is_default: makeDefault }; if (editingType) await updateType.mutateAsync({ id: editingType.id, body }); else await createType.mutateAsync(body); showToast(editingType ? 'Type updated.' : 'Type created.', { type: 'success' }); setEditingType(null); setTypeName(''); setTypeDescription(''); setMakeDefault(false) } catch (e) { fail(e) } }
  function editCategory(c: CMDBCategory) { setEditingCategory(c); setCategoryName(c.name); setCategoryDescription(c.description ?? '') }
  function editType(t: CMDBType) { setEditingType(t); setTypeName(t.name); setTypeDescription(t.description ?? ''); setTypeCategory(t.category_id); setTypeKind(t.asset_kind); setMakeDefault(t.is_default) }
  return <SlideOverPanel onClose={onClose}><div className="flex items-center justify-between border-b border-gray-200 px-6 py-4"><div><h2 className="font-semibold text-ink">Manage classifications</h2><p className="text-xs text-gray-500">Organization-wide categories and asset-kind types</p></div><button disabled={pending} onClick={onClose}><X className="h-5 w-5 text-gray-400" /></button></div><div className="flex-1 space-y-8 overflow-y-auto p-6">
    <section><h3 className="mb-3 text-sm font-semibold text-ink">Categories</h3><div className="space-y-2">{categories.map((c) => <div key={c.id} className="flex items-center justify-between rounded-md border border-gray-200 p-3"><div><div className="text-sm font-medium text-gray-800">{c.name}</div><div className="text-xs text-gray-400">{c.asset_count} assets · {c.types.length} types</div></div><div className="flex gap-2"><Button size="sm" variant="secondary" disabled={pending} onClick={() => editCategory(c)}>Edit</Button><Button size="sm" variant="secondary" isLoading={deleteCategory.isPending && deleteCategory.variables === c.id} disabled={pending || c.types.length > 0} onClick={async () => { try { await deleteCategory.mutateAsync(c.id); showToast('Category deleted.', { type: 'success' }) } catch (e) { fail(e) } }}>Delete</Button></div></div>)}</div><div className="mt-3 space-y-2 rounded-md bg-gray-50 p-3"><input value={categoryName} onChange={(e) => setCategoryName(e.target.value)} placeholder="Category name" className="w-full rounded border border-gray-300 px-3 py-2 text-sm" /><textarea value={categoryDescription} onChange={(e) => setCategoryDescription(e.target.value)} placeholder="Description (optional)" className="w-full rounded border border-gray-300 px-3 py-2 text-sm" /><div className="flex justify-end gap-2"><Button variant="secondary" disabled={pending} onClick={() => { setEditingCategory(null); setCategoryName(''); setCategoryDescription('') }}>Cancel</Button><Button isLoading={createCategory.isPending || updateCategory.isPending} disabled={!categoryName.trim()} onClick={saveCategory}>{editingCategory ? 'Save category' : 'Add category'}</Button></div></div></section>
    <section><h3 className="mb-3 text-sm font-semibold text-ink">Types</h3><div className="space-y-2">{categories.flatMap((c) => c.types.map((t) => <div key={t.id} className="flex items-center justify-between rounded-md border border-gray-200 p-3"><div><div className="text-sm font-medium text-gray-800">{t.name}{t.is_default && <span className="ml-2 rounded bg-brand-50 px-1.5 py-0.5 text-2xs text-brand-700">Default</span>}</div><div className="text-xs text-gray-400">{KIND_LABEL[t.asset_kind]} · {c.name} · {t.asset_count} assets</div></div><div className="flex gap-2"><Button size="sm" variant="secondary" disabled={pending} onClick={() => editType(t)}>Edit</Button><Button size="sm" variant="secondary" isLoading={deleteType.isPending && deleteType.variables === t.id} disabled={pending || t.is_default || t.asset_count > 0} onClick={async () => { try { await deleteType.mutateAsync(t.id); showToast('Type deleted.', { type: 'success' }) } catch (e) { fail(e) } }}>Delete</Button></div></div>))}</div><div className="mt-3 space-y-2 rounded-md bg-gray-50 p-3"><input value={typeName} onChange={(e) => setTypeName(e.target.value)} placeholder="Type name" className="w-full rounded border border-gray-300 px-3 py-2 text-sm" /><textarea value={typeDescription} onChange={(e) => setTypeDescription(e.target.value)} placeholder="Description (optional)" className="w-full rounded border border-gray-300 px-3 py-2 text-sm" /><div className="grid grid-cols-2 gap-2"><select value={typeCategory} onChange={(e) => setTypeCategory(e.target.value)} className="rounded border border-gray-300 px-3 py-2 text-sm">{categories.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}</select><select value={typeKind} disabled={Boolean(editingType)} onChange={(e) => setTypeKind(e.target.value as CMDBAssetKind)} className="rounded border border-gray-300 px-3 py-2 text-sm"><option value="endpoint">Endpoint</option><option value="agent">Agent</option><option value="tool">Tool</option></select></div><label className="flex items-center gap-2 text-sm text-gray-600"><input type="checkbox" checked={makeDefault} disabled={Boolean(editingType?.is_default)} onChange={(e) => setMakeDefault(e.target.checked)} />Make default for this asset kind</label>{editingType?.is_default && <p className="text-xs text-gray-500">This is the current default. To change it, make another type of this kind the default.</p>}<div className="flex justify-end gap-2"><Button variant="secondary" disabled={pending} onClick={() => { setEditingType(null); setTypeName(''); setTypeDescription(''); setMakeDefault(false) }}>Cancel</Button><Button isLoading={createType.isPending || updateType.isPending} disabled={!typeName.trim() || !typeCategory} onClick={saveType}>{editingType ? 'Save type' : 'Add type'}</Button></div></div></section>
  </div></SlideOverPanel>
}

function AssetClassificationPanel({ asset, categories, canEdit, onClose }: { asset: CMDBAsset; categories: CMDBCategory[]; canEdit: boolean; onClose: () => void }) {
  // B-252 C1: the logic now lives in AssetClassificationForm (shared with
  // Agent Detail's Classification tab). Only endpoint and tool rows open this
  // panel now, so it carries the honest transition note.
  return <SlideOverPanel onClose={onClose}><AssetClassificationForm asset={asset} categories={categories} canEdit={canEdit} header={{ onClose }} onCancel={onClose} onDone={onClose}
    note="Full detail page coming soon — classification available here for now." /></SlideOverPanel>
}
