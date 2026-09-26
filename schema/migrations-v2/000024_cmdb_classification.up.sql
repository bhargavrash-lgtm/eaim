-- B-196 Increment 2, Brief 1: organization-scoped CMDB classification.
-- A NULL asset ci_type_id means "resolve the sole default for this kind";
-- existing inventory therefore needs no destructive backfill.

CREATE TABLE ci_categories (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    description TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (id, org_id),
    UNIQUE (org_id, normalized_name)
);

CREATE TABLE ci_types (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    category_id UUID NOT NULL,
    asset_kind TEXT NOT NULL CHECK (asset_kind IN ('endpoint', 'agent', 'tool')),
    name TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    description TEXT,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (id, org_id),
    UNIQUE (org_id, normalized_name),
    CONSTRAINT ci_types_category_org_fk FOREIGN KEY (category_id, org_id)
        REFERENCES ci_categories(id, org_id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX ci_types_one_default_per_kind
    ON ci_types(org_id, asset_kind) WHERE is_default;
CREATE INDEX ci_types_category_idx ON ci_types(category_id);

CREATE OR REPLACE FUNCTION normalize_ci_name() RETURNS TRIGGER AS $$
BEGIN
    NEW.name := btrim(NEW.name);
    NEW.normalized_name := lower(regexp_replace(NEW.name, '\s+', ' ', 'g'));
    IF NEW.normalized_name = '' THEN
        RAISE EXCEPTION 'classification name must not be empty' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_ci_categories_normalize
    BEFORE INSERT OR UPDATE OF name ON ci_categories
    FOR EACH ROW EXECUTE FUNCTION normalize_ci_name();
CREATE TRIGGER trg_ci_types_normalize
    BEFORE INSERT OR UPDATE OF name ON ci_types
    FOR EACH ROW EXECUTE FUNCTION normalize_ci_name();
CREATE TRIGGER trg_ci_categories_updated_at
    BEFORE UPDATE ON ci_categories FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_ci_types_updated_at
    BEFORE UPDATE ON ci_types FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE OR REPLACE FUNCTION keep_ci_type_scope_immutable() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.asset_kind <> OLD.asset_kind THEN
        RAISE EXCEPTION 'asset_kind is immutable' USING ERRCODE = '23514';
    END IF;
    IF NEW.org_id <> OLD.org_id THEN
        RAISE EXCEPTION 'org_id is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_ci_types_kind_immutable
    BEFORE UPDATE OF asset_kind, org_id ON ci_types
    FOR EACH ROW EXECUTE FUNCTION keep_ci_type_scope_immutable();

CREATE OR REPLACE FUNCTION require_ci_default() RETURNS TRIGGER AS $$
DECLARE
    checked_org UUID := COALESCE(NEW.org_id, OLD.org_id);
    checked_kind TEXT := COALESCE(NEW.asset_kind, OLD.asset_kind);
BEGIN
    IF EXISTS (SELECT 1 FROM orgs WHERE id = checked_org)
       AND (SELECT count(*) FROM ci_types
            WHERE org_id = checked_org AND asset_kind = checked_kind AND is_default) <> 1 THEN
        RAISE EXCEPTION 'exactly one default CI type is required for org % kind %', checked_org, checked_kind
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_ci_types_require_default
    AFTER INSERT OR UPDATE OR DELETE ON ci_types
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION require_ci_default();

ALTER TABLE endpoints ADD COLUMN ci_type_id UUID;
ALTER TABLE gateway_agents ADD COLUMN ci_type_id UUID;
ALTER TABLE gateway_tools ADD COLUMN ci_type_id UUID;

ALTER TABLE endpoints ADD CONSTRAINT endpoints_ci_type_org_fk
    FOREIGN KEY (ci_type_id, org_id) REFERENCES ci_types(id, org_id) ON DELETE RESTRICT;
ALTER TABLE gateway_agents ADD CONSTRAINT gateway_agents_ci_type_org_fk
    FOREIGN KEY (ci_type_id, org_id) REFERENCES ci_types(id, org_id) ON DELETE RESTRICT;
ALTER TABLE gateway_tools ADD CONSTRAINT gateway_tools_ci_type_org_fk
    FOREIGN KEY (ci_type_id, org_id) REFERENCES ci_types(id, org_id) ON DELETE RESTRICT;

CREATE OR REPLACE FUNCTION check_asset_ci_type_kind() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.ci_type_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM ci_types
        WHERE id = NEW.ci_type_id AND org_id = NEW.org_id AND asset_kind = TG_ARGV[0]
    ) THEN
        RAISE EXCEPTION 'CI type does not belong to asset organization and kind'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_endpoints_ci_type_kind
    BEFORE INSERT OR UPDATE OF ci_type_id, org_id ON endpoints
    FOR EACH ROW EXECUTE FUNCTION check_asset_ci_type_kind('endpoint');
CREATE TRIGGER trg_gateway_agents_ci_type_kind
    BEFORE INSERT OR UPDATE OF ci_type_id, org_id ON gateway_agents
    FOR EACH ROW EXECUTE FUNCTION check_asset_ci_type_kind('agent');
CREATE TRIGGER trg_gateway_tools_ci_type_kind
    BEFORE INSERT OR UPDATE OF ci_type_id, org_id ON gateway_tools
    FOR EACH ROW EXECUTE FUNCTION check_asset_ci_type_kind('tool');

CREATE OR REPLACE FUNCTION seed_default_ci_taxonomy(seed_org UUID) RETURNS VOID AS $$
DECLARE
    endpoint_category UUID := uuid_generate_v5(seed_org, 'cmdb-category:end-user-compute');
    ai_category UUID := uuid_generate_v5(seed_org, 'cmdb-category:ai-systems');
    integrations_category UUID := uuid_generate_v5(seed_org, 'cmdb-category:integrations');
BEGIN
    INSERT INTO ci_categories(id, org_id, name, normalized_name, sort_order)
    VALUES
        (endpoint_category, seed_org, 'End-user compute', 'end-user compute', 10),
        (ai_category, seed_org, 'AI systems', 'ai systems', 20),
        (integrations_category, seed_org, 'Integrations', 'integrations', 30)
    ON CONFLICT (org_id, normalized_name) DO NOTHING;

    SELECT id INTO endpoint_category FROM ci_categories WHERE org_id = seed_org AND normalized_name = 'end-user compute';
    SELECT id INTO ai_category FROM ci_categories WHERE org_id = seed_org AND normalized_name = 'ai systems';
    SELECT id INTO integrations_category FROM ci_categories WHERE org_id = seed_org AND normalized_name = 'integrations';

    INSERT INTO ci_types(id, org_id, category_id, asset_kind, name, normalized_name, is_default)
    VALUES
        (uuid_generate_v5(seed_org, 'cmdb-type:endpoint:default'), seed_org, endpoint_category, 'endpoint', 'Endpoint', 'endpoint', TRUE),
        (uuid_generate_v5(seed_org, 'cmdb-type:agent:default'), seed_org, ai_category, 'agent', 'Governed agent', 'governed agent', TRUE),
        (uuid_generate_v5(seed_org, 'cmdb-type:tool:default'), seed_org, integrations_category, 'tool', 'Connector', 'connector', TRUE)
    ON CONFLICT (org_id, normalized_name) DO NOTHING;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION seed_default_ci_taxonomy_for_org() RETURNS TRIGGER AS $$
BEGIN
    PERFORM seed_default_ci_taxonomy(NEW.id);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_orgs_seed_default_ci_taxonomy
    AFTER INSERT ON orgs
    FOR EACH ROW EXECUTE FUNCTION seed_default_ci_taxonomy_for_org();

SELECT seed_default_ci_taxonomy(id) FROM orgs;
