#!/usr/bin/env bash
# seed-db.sh — Seeds the EAMI database with demo data for development.
#
# Usage:
#   ./scripts/seed-db.sh
#
# Requires the postgres container to be healthy first:
#   docker-compose up -d postgres && docker-compose exec postgres pg_isready
#
# The DATABASE_URL env var can override the default connection string.

set -euo pipefail

DB_URL="${DATABASE_URL:-postgresql://eami_app:devpassword@localhost:5432/eami}"

echo "Seeding EAMI database at ${DB_URL%%@*}@... (credentials hidden)"

psql "$DB_URL" <<'SQL'
-- ─────────────────────────────────────────────────────────
-- Demo org
-- ─────────────────────────────────────────────────────────
INSERT INTO orgs (id, name, slug, plan) VALUES
    ('00000000-0000-0000-0000-000000000001', 'Acme Corp', 'acme', 'business')
ON CONFLICT DO NOTHING;

-- ─────────────────────────────────────────────────────────
-- Demo users
-- ─────────────────────────────────────────────────────────
INSERT INTO users (id, org_id, email, name, role, password_hash) VALUES
    ('00000000-0000-0000-0000-000000000010',
     '00000000-0000-0000-0000-000000000001',
     'admin@acme.com', 'Admin User', 'admin',
     '$2a$10$dev_placeholder_hash_not_for_prod')
ON CONFLICT DO NOTHING;

INSERT INTO users (id, org_id, email, name, role, password_hash) VALUES
    ('00000000-0000-0000-0000-000000000011',
     '00000000-0000-0000-0000-000000000001',
     'operator@acme.com', 'Operator User', 'operator',
     '$2a$10$dev_placeholder_hash_not_for_prod')
ON CONFLICT DO NOTHING;

-- ─────────────────────────────────────────────────────────
-- Demo gateway agents
-- ─────────────────────────────────────────────────────────
INSERT INTO gateway_agents (org_id, name, model, owner, scope, risk_tier, status) VALUES
    ('00000000-0000-0000-0000-000000000001',
     'claude-support-01', 'claude-sonnet-4-6',
     'Support team', 'Triage and reply to support tickets', 'low', 'active'),
    ('00000000-0000-0000-0000-000000000001',
     'gpt-dataops', 'gpt-4o',
     'Data team', 'Read and write database records for data operations', 'high', 'active'),
    ('00000000-0000-0000-0000-000000000001',
     'claude-codebot', 'claude-sonnet-4-6',
     'Engineering', 'Open and review pull requests, read repo contents', 'medium', 'active')
ON CONFLICT DO NOTHING;

-- ─────────────────────────────────────────────────────────
-- Demo policies
-- ─────────────────────────────────────────────────────────
INSERT INTO policies (org_id, name, description, priority, action, status) VALUES
    ('00000000-0000-0000-0000-000000000001',
     'Block all production data deletion',
     'Deny any DELETE operation on production data by any agent',
     1, 'deny', 'active'),
    ('00000000-0000-0000-0000-000000000001',
     'Escalate bulk record updates',
     'Require human approval for bulk updates affecting more than 100 records',
     2, 'escalate', 'active'),
    ('00000000-0000-0000-0000-000000000001',
     'Allow low-risk read operations',
     'Allow read-only operations for agents with low risk tier',
     10, 'allow', 'active')
ON CONFLICT DO NOTHING;

-- ─────────────────────────────────────────────────────────
-- Demo model pricing (already in schema, but re-insert for safety)
-- ─────────────────────────────────────────────────────────
INSERT INTO model_pricing (model, cost_per_1k_in, cost_per_1k_out) VALUES
    ('claude-opus-4-6',    0.015,   0.075),
    ('claude-sonnet-4-6',  0.003,   0.015),
    ('claude-haiku-4-5',   0.00025, 0.00125),
    ('gpt-4o',             0.005,   0.015),
    ('gpt-4o-mini',        0.00015, 0.0006),
    ('gpt-4-turbo',        0.010,   0.030)
ON CONFLICT (model) DO NOTHING;

-- ─────────────────────────────────────────────────────────
-- Demo gateway node (the local dev instance)
-- ─────────────────────────────────────────────────────────
INSERT INTO gateway_nodes (org_id, name, role, status, address, hostname, version) VALUES
    ('00000000-0000-0000-0000-000000000001',
     'gateway-dev-01', 'primary', 'healthy',
     'localhost:8080', 'localhost', 'dev')
ON CONFLICT DO NOTHING;
SQL

echo "Seed complete."
