CREATE TABLE IF NOT EXISTS hypotheses (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    status TEXT NOT NULL,
    source_path TEXT NOT NULL,
    content_sha256 TEXT NOT NULL,
    spec_json JSONB NOT NULL,
    raw_yaml TEXT NOT NULL,
    imported_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT hypotheses_unique_name_version UNIQUE (name, version),
    CONSTRAINT hypotheses_status_import_scope CHECK (status = 'DRAFT'),
    CONSTRAINT hypotheses_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT hypotheses_version_not_blank CHECK (btrim(version) <> ''),
    CONSTRAINT hypotheses_source_path_not_blank CHECK (btrim(source_path) <> ''),
    CONSTRAINT hypotheses_raw_yaml_not_blank CHECK (btrim(raw_yaml) <> ''),
    CONSTRAINT hypotheses_content_sha256_hex CHECK (content_sha256 ~ '^[a-f0-9]{64}$'),
    CONSTRAINT hypotheses_spec_json_object CHECK (jsonb_typeof(spec_json) = 'object')
);

CREATE INDEX IF NOT EXISTS hypotheses_status_imported_at_idx
    ON hypotheses (status, imported_at DESC);

CREATE INDEX IF NOT EXISTS hypotheses_imported_at_idx
    ON hypotheses (imported_at DESC);
