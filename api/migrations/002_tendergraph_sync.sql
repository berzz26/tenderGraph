-- TenderGraph Phase 3 control plane.
-- Additive only: no existing source table, column, index or record is changed.

CREATE TABLE IF NOT EXISTS tg_datasets (
    dataset_name text PRIMARY KEY,
    source_table text NOT NULL,
    strategy text NOT NULL CHECK (strategy IN ('timestamp_pk', 'created_pk', 'pk')),
    enabled boolean NOT NULL DEFAULT true,
    batch_size integer NOT NULL DEFAULT 500 CHECK (batch_size BETWEEN 1 AND 1000),
    description text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tg_sync_watermarks (
    dataset_name text PRIMARY KEY REFERENCES tg_datasets(dataset_name),
    cursor_time timestamptz,
    cursor_pk text,
    last_run_id bigint,
    status text NOT NULL DEFAULT 'NEVER_RUN'
        CHECK (status IN ('NEVER_RUN', 'RUNNING', 'SUCCEEDED', 'FAILED')),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tg_sync_runs (
    run_id bigserial PRIMARY KEY,
    dataset_name text NOT NULL REFERENCES tg_datasets(dataset_name),
    mode text NOT NULL CHECK (mode IN ('initial', 'incremental', 'selected', 'retry')),
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    status text NOT NULL DEFAULT 'RUNNING'
        CHECK (status IN ('RUNNING', 'SUCCEEDED', 'FAILED', 'PARTIAL')),
    records_scanned bigint NOT NULL DEFAULT 0,
    records_projected bigint NOT NULL DEFAULT 0,
    records_failed bigint NOT NULL DEFAULT 0,
    error text NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS tg_sync_runs_dataset_started_idx
    ON tg_sync_runs(dataset_name, started_at DESC);

CREATE TABLE IF NOT EXISTS tg_sync_batches (
    batch_id bigserial PRIMARY KEY,
    run_id bigint NOT NULL REFERENCES tg_sync_runs(run_id) ON DELETE CASCADE,
    dataset_name text NOT NULL REFERENCES tg_datasets(dataset_name),
    cursor_before_time timestamptz,
    cursor_before_pk text,
    cursor_after_time timestamptz,
    cursor_after_pk text,
    row_count integer NOT NULL DEFAULT 0,
    attempt_count integer NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'RUNNING'
        CHECK (status IN ('RUNNING', 'SUCCEEDED', 'FAILED')),
    error text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);

CREATE INDEX IF NOT EXISTS tg_sync_batches_run_idx
    ON tg_sync_batches(run_id, batch_id);
CREATE INDEX IF NOT EXISTS tg_sync_batches_failed_idx
    ON tg_sync_batches(dataset_name, status, batch_id);

CREATE TABLE IF NOT EXISTS tg_reconciliation_runs (
    reconciliation_id bigserial PRIMARY KEY,
    dataset_name text NOT NULL REFERENCES tg_datasets(dataset_name),
    mode text NOT NULL DEFAULT 'detect' CHECK (mode IN ('detect', 'repair')),
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    status text NOT NULL DEFAULT 'RUNNING'
        CHECK (status IN ('RUNNING', 'SUCCEEDED', 'FAILED')),
    missing_nodes bigint NOT NULL DEFAULT 0,
    missing_edges bigint NOT NULL DEFAULT 0,
    stale_nodes bigint NOT NULL DEFAULT 0,
    invalid_provenance bigint NOT NULL DEFAULT 0,
    duplicate_logical_edges bigint NOT NULL DEFAULT 0,
    examples jsonb NOT NULL DEFAULT '[]'::jsonb,
    error text NOT NULL DEFAULT ''
);

-- Seed only the approved Phase 3 datasets. This is control metadata, not
-- source data. INSERTs are idempotent so the migration is safe to re-apply.
INSERT INTO tg_datasets(dataset_name, source_table, strategy, batch_size, description)
VALUES
    ('contractor_v2', 'contractor_v2', 'timestamp_pk', 500, 'Vendor registry nodes'),
    ('entities', 'entities', 'timestamp_pk', 500, 'CorporateEntity nodes'),
    ('entity_aliases', 'entity_aliases', 'created_pk', 500, 'Alias nodes and HAS_ALIAS edges'),
    ('entity_gst', 'entity_gst', 'timestamp_pk', 500, 'GST registration nodes and edges'),
    ('tenders', 'tenders', 'timestamp_pk', 500, 'Tender, organization and contractor assignment'),
    ('tender_participants', 'tender_participants', 'timestamp_pk', 500, 'Bid and co-bid observations'),
    ('tender_extracted_meta', 'tender_extracted_meta', 'timestamp_pk', 500, 'Award and winner evidence')
ON CONFLICT (dataset_name) DO NOTHING;

INSERT INTO tg_sync_watermarks(dataset_name)
SELECT dataset_name FROM tg_datasets
ON CONFLICT (dataset_name) DO NOTHING;
