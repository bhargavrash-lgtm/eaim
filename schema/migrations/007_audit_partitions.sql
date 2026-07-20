-- Migration: 007_audit_partitions.sql
-- Adds audit_log monthly partitions for Aug 2026 – Dec 2027.
-- Also installs a pg_cron job to auto-create the next month's partition
-- on the 20th of each month (gives 10 days of lead time).
--
-- Apply against an existing DB: psql -f schema/migrations/007_audit_partitions.sql
-- Safe to re-run (IF NOT EXISTS guards each partition).

-- ── Aug–Dec 2026 ─────────────────────────────────────────────────────────────

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE c.relname = 'audit_log_2026_08'
  ) THEN
    EXECUTE 'CREATE TABLE audit_log_2026_08 PARTITION OF audit_log
             FOR VALUES FROM (''2026-08-01'') TO (''2026-09-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2026_09') THEN
    EXECUTE 'CREATE TABLE audit_log_2026_09 PARTITION OF audit_log
             FOR VALUES FROM (''2026-09-01'') TO (''2026-10-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2026_10') THEN
    EXECUTE 'CREATE TABLE audit_log_2026_10 PARTITION OF audit_log
             FOR VALUES FROM (''2026-10-01'') TO (''2026-11-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2026_11') THEN
    EXECUTE 'CREATE TABLE audit_log_2026_11 PARTITION OF audit_log
             FOR VALUES FROM (''2026-11-01'') TO (''2026-12-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2026_12') THEN
    EXECUTE 'CREATE TABLE audit_log_2026_12 PARTITION OF audit_log
             FOR VALUES FROM (''2026-12-01'') TO (''2027-01-01'')';
  END IF;
END $$;

-- ── 2027 ─────────────────────────────────────────────────────────────────────

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_01') THEN
    EXECUTE 'CREATE TABLE audit_log_2027_01 PARTITION OF audit_log
             FOR VALUES FROM (''2027-01-01'') TO (''2027-02-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_02') THEN
    EXECUTE 'CREATE TABLE audit_log_2027_02 PARTITION OF audit_log
             FOR VALUES FROM (''2027-02-01'') TO (''2027-03-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_03') THEN
    EXECUTE 'CREATE TABLE audit_log_2027_03 PARTITION OF audit_log
             FOR VALUES FROM (''2027-03-01'') TO (''2027-04-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_04') THEN
    EXECUTE 'CREATE TABLE audit_log_2027_04 PARTITION OF audit_log
             FOR VALUES FROM (''2027-04-01'') TO (''2027-05-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_05') THEN
    EXECUTE 'CREATE TABLE audit_log_2027_05 PARTITION OF audit_log
             FOR VALUES FROM (''2027-05-01'') TO (''2027-06-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_06') THEN
    EXECUTE 'CREATE TABLE audit_log_2027_06 PARTITION OF audit_log
             FOR VALUES FROM (''2027-06-01'') TO (''2027-07-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_07') THEN
    EXECUTE 'CREATE TABLE audit_log_2027_07 PARTITION OF audit_log
             FOR VALUES FROM (''2027-07-01'') TO (''2027-08-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_08') THEN
    EXECUTE 'CREATE TABLE audit_log_2027_08 PARTITION OF audit_log
             FOR VALUES FROM (''2027-08-01'') TO (''2027-09-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_09') THEN
    EXECUTE 'CREATE TABLE audit_log_2027_09 PARTITION OF audit_log
             FOR VALUES FROM (''2027-09-01'') TO (''2027-10-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_10') THEN
    EXECUTE 'CREATE TABLE audit_log_2027_10 PARTITION OF audit_log
             FOR VALUES FROM (''2027-10-01'') TO (''2027-11-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_11') THEN
    EXECUTE 'CREATE TABLE audit_log_2027_11 PARTITION OF audit_log
             FOR VALUES FROM (''2027-11-01'') TO (''2027-12-01'')';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_12') THEN
    EXECUTE 'CREATE TABLE audit_log_2027_12 PARTITION OF audit_log
             FOR VALUES FROM (''2027-12-01'') TO (''2028-01-01'')';
  END IF;
END $$;

-- ── pg_cron auto-partition (runs on the 20th of each month) ──────────────────
-- The timescale/timescaledb-ha image ships pg_cron.
-- docker-compose enables it via:
--   command: postgres -c shared_preload_libraries='timescaledb,pg_cron' -c cron.database_name=eami
-- This migration is idempotent and safe to re-run.

CREATE EXTENSION IF NOT EXISTS pg_cron;

CREATE OR REPLACE FUNCTION create_next_audit_partition() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    next_month      DATE := date_trunc('month', NOW() + INTERVAL '1 month');
    partition_name  TEXT := 'audit_log_' || to_char(next_month, 'YYYY_MM');
    range_start     TEXT := to_char(next_month, 'YYYY-MM-DD');
    range_end       TEXT := to_char(next_month + INTERVAL '1 month', 'YYYY-MM-DD');
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_class WHERE relname = partition_name
    ) THEN
        EXECUTE format(
            'CREATE TABLE %I PARTITION OF audit_log FOR VALUES FROM (%L) TO (%L)',
            partition_name, range_start, range_end
        );
        RAISE NOTICE 'Created audit_log partition: %', partition_name;
    END IF;
END;
$$;

-- Schedule: run on the 20th of every month at 02:00 UTC.
-- Creates next month's partition 10 days before it is needed.
-- cron.schedule is idempotent when the job name already exists.
SELECT cron.schedule(
    'create-audit-partition',
    '0 2 20 * *',
    'SELECT create_next_audit_partition()'
);
