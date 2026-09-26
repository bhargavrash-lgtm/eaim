DROP TRIGGER IF EXISTS trg_orgs_seed_default_ci_taxonomy ON orgs;
DROP FUNCTION IF EXISTS seed_default_ci_taxonomy_for_org();
DROP FUNCTION IF EXISTS seed_default_ci_taxonomy(UUID);

DROP TRIGGER IF EXISTS trg_gateway_tools_ci_type_kind ON gateway_tools;
DROP TRIGGER IF EXISTS trg_gateway_agents_ci_type_kind ON gateway_agents;
DROP TRIGGER IF EXISTS trg_endpoints_ci_type_kind ON endpoints;
DROP FUNCTION IF EXISTS check_asset_ci_type_kind();

ALTER TABLE gateway_tools DROP COLUMN IF EXISTS ci_type_id;
ALTER TABLE gateway_agents DROP COLUMN IF EXISTS ci_type_id;
ALTER TABLE endpoints DROP COLUMN IF EXISTS ci_type_id;

DROP TABLE IF EXISTS ci_types;
DROP TABLE IF EXISTS ci_categories;
DROP FUNCTION IF EXISTS require_ci_default();
DROP FUNCTION IF EXISTS keep_ci_type_scope_immutable();
DROP FUNCTION IF EXISTS normalize_ci_name();
