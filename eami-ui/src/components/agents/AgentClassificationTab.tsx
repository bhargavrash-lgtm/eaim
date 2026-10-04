// AgentClassificationTab -- Agent Detail's Classification tab (B-252 C1;
// DESIGN_SYSTEM.md §7.7). The logic lives in AssetClassificationTab, shared
// with Endpoint Detail (C2); behaviour is unchanged.
import { AssetClassificationTab } from '@/components/cmdb/AssetClassificationTab'

export function AgentClassificationTab({ agentId }: { agentId: string }) {
  return <AssetClassificationTab kind="agent" id={agentId} noun="agent" />
}
