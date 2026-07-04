-- =============================================================================
-- TriageOS - Database Initialisation Script
-- Independent portfolio demo. Seed data is fictional and synthetic.
-- Apply via psql or a hosted PostgreSQL SQL editor.
--
-- Phase 1 (Foundation: Gateway + Identity + Tenancy) added:
--   - organizations / users (identity-service owns these)
--   - org_id on every domain table, enforced by Postgres RLS (not app filters)
--   - one seeded demo organization all existing seed rows are scoped to
-- =============================================================================

-- ---------------------------------------------------------------------------
-- Extensions
-- ---------------------------------------------------------------------------

-- pgcrypto: enables gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- pgvector: enables VECTOR column type and cosine-similarity operator (<=>)
CREATE EXTENSION IF NOT EXISTS vector;

-- ---------------------------------------------------------------------------
-- ENUM types
-- ---------------------------------------------------------------------------

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'triage_resolution') THEN
        CREATE TYPE triage_resolution AS ENUM (
            'AI_AUTO',
            'NURSE_APPROVED',
            'NURSE_CORRECTED',
            'DOCTOR_CORRECTED'
        );
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'queue_status') THEN
        CREATE TYPE queue_status AS ENUM (
            'PENDING',
            'RESOLVED',
            'TIMEOUT'
        );
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'user_role') THEN
        CREATE TYPE user_role AS ENUM (
            'OWNER',
            'ADMIN',
            'NURSE',
            'DOCTOR'
        );
    END IF;
END $$;

-- =============================================================================
-- Identity / Tenancy (owned by identity-service)
-- =============================================================================

CREATE TABLE IF NOT EXISTS organizations (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(255) NOT NULL,
    slug       VARCHAR(100) UNIQUE NOT NULL,
    plan_tier  VARCHAR(50)  NOT NULL DEFAULT 'demo',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- users.auth_user_id is the Supabase Auth user id (auth.users.id) – identity-service
-- maps that opaque identity to a tenant + role. One row per staff member; patients
-- never get a row here (see "Patient sessions" below).
CREATE TABLE IF NOT EXISTS users (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    auth_user_id UUID        NOT NULL UNIQUE,
    email        VARCHAR(255) NOT NULL,
    role         user_role   NOT NULL DEFAULT 'NURSE',
    created_at   TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_org ON users (org_id);

-- Seed: one fictional demo clinic organization every table below is scoped to.
INSERT INTO organizations (name, slug, plan_tier) VALUES
    ('Evergreen Clinic Network', 'evergreen-demo', 'demo')
ON CONFLICT (slug) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Table: departments
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS departments (
    id     UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    code   VARCHAR(50)  NOT NULL,
    name   VARCHAR(255) NOT NULL,
    UNIQUE (org_id, code)
);

-- ---------------------------------------------------------------------------
-- Table: triage_logs  (Semantic Memory / Flywheel)
--
-- Every triage attempt is logged here.
-- symptom_embedding is used for future semantic retrieval and model improvement.
-- final_dept / resolution_type are back-filled by the nurse resolution flow.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS triage_logs (
    id                  UUID      PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              UUID      NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    raw_symptoms        TEXT      NOT NULL,
    symptom_embedding   VECTOR(1536),                   -- OpenAI text-embedding-3-small
    ai_suggested_dept   VARCHAR(255),
    confidence          FLOAT,
    final_dept          VARCHAR(255),                   -- filled in after nurse review
    resolution_type     triage_resolution,              -- filled in after nurse review
    created_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Index for fast ANN search on symptom embeddings (cosine distance)
CREATE INDEX IF NOT EXISTS idx_triage_logs_embedding
    ON triage_logs
    USING ivfflat (symptom_embedding vector_cosine_ops)
    WITH (lists = 100);

CREATE INDEX IF NOT EXISTS idx_triage_logs_org ON triage_logs (org_id);

-- ---------------------------------------------------------------------------
-- Table: human_triage_queue
--
-- Low-confidence cases (confidence < 85) are inserted here.
-- Nurses see these on their dashboard and approve or correct the routing.
-- SLA: items older than 3 minutes without resolution are marked TIMEOUT.
--
-- patient_id holds the anonymous, gateway-issued patient-session id (a UUID),
-- never client-supplied free text (Phase 1: "patient sessions are anonymous
-- but token-bound"). Column name kept as-is to avoid unnecessary churn.
--
-- triage_log_id (Phase 3): the exact triage_logs row this item was created
-- from, set once at insert time by queue-service. Fixes the monolith's old
-- "most recent triage_logs row with a matching department" heuristic in
-- resolve_queue_item — queue-service's resolver uses this FK directly and
-- only falls back to the heuristic for pre-Phase-3 rows that lack it.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS human_triage_queue (
    id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           UUID         NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    patient_id       VARCHAR(255) NOT NULL,
    clinical_summary TEXT         NOT NULL,
    suggested_dept   VARCHAR(255),
    triage_log_id    UUID         REFERENCES triage_logs(id) ON DELETE SET NULL,
    status           queue_status DEFAULT 'PENDING',
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Index for fast lookup of PENDING items (nurse dashboard polling)
CREATE INDEX IF NOT EXISTS idx_human_triage_queue_status
    ON human_triage_queue (status, created_at ASC);

CREATE INDEX IF NOT EXISTS idx_human_triage_queue_triage_log_id
    ON human_triage_queue (triage_log_id);

CREATE INDEX IF NOT EXISTS idx_human_triage_queue_org ON human_triage_queue (org_id);

-- ---------------------------------------------------------------------------
-- Table: red_flags
--
-- Stores Vietnamese emergency keyword embeddings for semantic red-flag detection.
-- org_id NULL = global default set, shared by every tenant (see §4 data model).
-- Phase 1 only seeds the global default set; per-org overrides are a later
-- extension, not required by this phase.
--
-- HOW TO POPULATE:
--   Embeddings cannot be pre-inserted in plain SQL because they must be
--   generated at runtime via the OpenAI Embeddings API.
--
--   After running this migration, call the seed endpoint once (requires an
--   ADMIN/OWNER-scoped request through the gateway):
--
--       POST /api/v1/admin/seed-red-flags
--
--   That endpoint will:
--     1. Iterate over all 15 Vietnamese emergency keywords defined in config.py.
--     2. Call OpenAI text-embedding-3-small to generate a 1536-dim vector for each.
--     3. Upsert each row via ON CONFLICT (keyword) DO UPDATE – so it is safe
--        to call multiple times (e.g. after switching embedding models).
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS red_flags (
    id        UUID  PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id    UUID  REFERENCES organizations(id) ON DELETE CASCADE,
    keyword   TEXT  UNIQUE NOT NULL,               -- Vietnamese emergency term
    embedding VECTOR(1536)                          -- populated by /admin/seed-red-flags
);

-- Index for fast ANN cosine similarity search against symptom embeddings
CREATE INDEX IF NOT EXISTS idx_red_flags_embedding
    ON red_flags
    USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 10);   -- small table, 10 lists is sufficient

-- ---------------------------------------------------------------------------
-- Seed: departments
-- ---------------------------------------------------------------------------

INSERT INTO departments (org_id, code, name)
SELECT o.id, d.code, d.name
FROM organizations o
CROSS JOIN (VALUES
    ('TIM_MACH',          'Nội Tim Mạch'),
    ('NGOAI_TH',          'Ngoại Tiêu hoá'),
    ('THAN_KINH',         'Nội Thần Kinh'),
    ('SAN_PHU',           'Sản Phụ Khoa'),
    ('NHI',               'Nhi Khoa'),
    ('DA_LIEU',           'Da liễu'),
    ('MAT',               'Nhãn Khoa'),
    ('TAI_MUI_HONG',      'Tai Mũi Họng'),
    ('CO_XUONG_KHOP',     'Cơ Xương Khớp'),
    ('NGOAI_CHINH_HINH',  'Ngoại Chỉnh hình')
) AS d(code, name)
WHERE o.slug = 'evergreen-demo'
ON CONFLICT (org_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Table: clinics
-- Fictional demo clinics used for "nearest clinic" lookup.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS clinics (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    address         TEXT         NOT NULL,
    department_code VARCHAR(50)  NOT NULL,
    FOREIGN KEY (org_id, department_code) REFERENCES departments (org_id, code) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_clinics_dept ON clinics (org_id, department_code);

-- ---------------------------------------------------------------------------
-- Table: doctors
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS doctors (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    specialty       VARCHAR(255) NOT NULL,
    department_code VARCHAR(50)  NOT NULL,
    FOREIGN KEY (org_id, department_code) REFERENCES departments (org_id, code) ON DELETE CASCADE,
    UNIQUE (org_id, id) -- lets appointments FK on (org_id, doctor_id): a plain
                        -- FK on id alone would let a patient book a doctor
                        -- from a different tenant while org_id still matches
                        -- their own session, defeating RLS isolation.
);

CREATE INDEX IF NOT EXISTS idx_doctors_dept ON doctors (org_id, department_code);

-- ---------------------------------------------------------------------------
-- Table: appointments
--
-- patient_id holds the anonymous, gateway-issued patient-session id (see
-- human_triage_queue comment above – same convention).
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS appointments (
    id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           UUID         NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    patient_id       VARCHAR(255) NOT NULL,
    doctor_id        UUID         NOT NULL,
    department_code  VARCHAR(50)  NOT NULL,
    appointment_time TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    FOREIGN KEY (org_id, doctor_id) REFERENCES doctors (org_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_appointments_org ON appointments (org_id);

-- ---------------------------------------------------------------------------
-- Seed: clinics (one per department)
-- ---------------------------------------------------------------------------

INSERT INTO clinics (org_id, name, address, department_code)
SELECT o.id, c.name, c.address, c.department_code
FROM organizations o
CROSS JOIN (VALUES
    -- Evergreen Midtown Clinic
    ('Evergreen Midtown Clinic - Nội Tim Mạch',     '100 Demo Care Way, Ba Dinh, Ha Noi',             'TIM_MACH'),
    ('Evergreen Midtown Clinic - Ngoại Tiêu hoá',   '100 Demo Care Way, Ba Dinh, Ha Noi',             'NGOAI_TH'),
    ('Evergreen Midtown Clinic - Nội Thần Kinh',    '100 Demo Care Way, Ba Dinh, Ha Noi',             'THAN_KINH'),
    ('Evergreen Midtown Clinic - Sản Phụ Khoa',     '100 Demo Care Way, Ba Dinh, Ha Noi',             'SAN_PHU'),
    ('Evergreen Midtown Clinic - Nhi Khoa',         '100 Demo Care Way, Ba Dinh, Ha Noi',             'NHI'),
    ('Evergreen Midtown Clinic - Da liễu',          '100 Demo Care Way, Ba Dinh, Ha Noi',             'DA_LIEU'),
    ('Evergreen Midtown Clinic - Nhãn Khoa',        '100 Demo Care Way, Ba Dinh, Ha Noi',             'MAT'),
    ('Evergreen Midtown Clinic - Tai Mũi Họng',     '100 Demo Care Way, Ba Dinh, Ha Noi',             'TAI_MUI_HONG'),
    ('Evergreen Midtown Clinic - Cơ Xương Khớp',    '100 Demo Care Way, Ba Dinh, Ha Noi',             'CO_XUONG_KHOP'),
    ('Evergreen Midtown Clinic - Ngoại Chỉnh hình', '100 Demo Care Way, Ba Dinh, Ha Noi',             'NGOAI_CHINH_HINH'),
    -- Evergreen Riverside Clinic
    ('Evergreen Riverside Clinic - Nội Tim Mạch',     '200 Sample Health Street, Cau Giay, Ha Noi',   'TIM_MACH'),
    ('Evergreen Riverside Clinic - Ngoại Tiêu hoá',   '200 Sample Health Street, Cau Giay, Ha Noi',   'NGOAI_TH'),
    ('Evergreen Riverside Clinic - Nội Thần Kinh',    '200 Sample Health Street, Cau Giay, Ha Noi',   'THAN_KINH'),
    ('Evergreen Riverside Clinic - Sản Phụ Khoa',     '200 Sample Health Street, Cau Giay, Ha Noi',   'SAN_PHU'),
    ('Evergreen Riverside Clinic - Nhi Khoa',         '200 Sample Health Street, Cau Giay, Ha Noi',   'NHI'),
    ('Evergreen Riverside Clinic - Da liễu',          '200 Sample Health Street, Cau Giay, Ha Noi',   'DA_LIEU'),
    ('Evergreen Riverside Clinic - Nhãn Khoa',        '200 Sample Health Street, Cau Giay, Ha Noi',   'MAT'),
    ('Evergreen Riverside Clinic - Tai Mũi Họng',     '200 Sample Health Street, Cau Giay, Ha Noi',   'TAI_MUI_HONG'),
    ('Evergreen Riverside Clinic - Cơ Xương Khớp',    '200 Sample Health Street, Cau Giay, Ha Noi',   'CO_XUONG_KHOP'),
    ('Evergreen Riverside Clinic - Ngoại Chỉnh hình', '200 Sample Health Street, Cau Giay, Ha Noi',   'NGOAI_CHINH_HINH'),
    -- Evergreen Lakeside Clinic
    ('Evergreen Lakeside Clinic - Nội Tim Mạch',     '300 Fictional Wellness Avenue, Long Bien, Ha Noi', 'TIM_MACH'),
    ('Evergreen Lakeside Clinic - Ngoại Tiêu hoá',   '300 Fictional Wellness Avenue, Long Bien, Ha Noi', 'NGOAI_TH'),
    ('Evergreen Lakeside Clinic - Nội Thần Kinh',    '300 Fictional Wellness Avenue, Long Bien, Ha Noi', 'THAN_KINH'),
    ('Evergreen Lakeside Clinic - Sản Phụ Khoa',     '300 Fictional Wellness Avenue, Long Bien, Ha Noi', 'SAN_PHU'),
    ('Evergreen Lakeside Clinic - Nhi Khoa',         '300 Fictional Wellness Avenue, Long Bien, Ha Noi', 'NHI'),
    ('Evergreen Lakeside Clinic - Da liễu',          '300 Fictional Wellness Avenue, Long Bien, Ha Noi', 'DA_LIEU'),
    ('Evergreen Lakeside Clinic - Nhãn Khoa',        '300 Fictional Wellness Avenue, Long Bien, Ha Noi', 'MAT'),
    ('Evergreen Lakeside Clinic - Tai Mũi Họng',     '300 Fictional Wellness Avenue, Long Bien, Ha Noi', 'TAI_MUI_HONG'),
    ('Evergreen Lakeside Clinic - Cơ Xương Khớp',    '300 Fictional Wellness Avenue, Long Bien, Ha Noi', 'CO_XUONG_KHOP'),
    ('Evergreen Lakeside Clinic - Ngoại Chỉnh hình', '300 Fictional Wellness Avenue, Long Bien, Ha Noi', 'NGOAI_CHINH_HINH')
) AS c(name, address, department_code)
WHERE o.slug = 'evergreen-demo'
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Seed: doctors (~2 per department)
-- ---------------------------------------------------------------------------

INSERT INTO doctors (org_id, name, specialty, department_code)
SELECT o.id, d.name, d.specialty, d.department_code
FROM organizations o
CROSS JOIN (VALUES
    ('BS. An Green',       'Tim mạch can thiệp',          'TIM_MACH'),
    ('BS. Binh River',     'Rối loạn nhịp tim',           'TIM_MACH'),
    ('BS. Cuong Stone',    'Phẫu thuật tiêu hoá',         'NGOAI_TH'),
    ('BS. Dung Maple',     'Nội soi tiêu hoá',            'NGOAI_TH'),
    ('BS. Duc Vale',       'Thần kinh học lâm sàng',      'THAN_KINH'),
    ('BS. Lan North',      'Đau đầu và đột quỵ',          'THAN_KINH'),
    ('BS. Mai Field',      'Sản khoa',                    'SAN_PHU'),
    ('BS. Nam West',       'Phụ khoa - Nội tiết sinh sản','SAN_PHU'),
    ('BS. Ha Clear',       'Nhi tổng quát',               'NHI'),
    ('BS. Huy Bright',     'Nhi sơ sinh',                 'NHI'),
    ('BS. Kim Dawn',       'Da liễu thẩm mỹ',             'DA_LIEU'),
    ('BS. Long Pine',      'Dị ứng - miễn dịch da',       'DA_LIEU'),
    ('BS. Minh Lake',      'Nhãn khoa tổng quát',         'MAT'),
    ('BS. Nghia Hill',     'Phẫu thuật mắt',              'MAT'),
    ('BS. Oanh Cedar',     'Tai mũi họng tổng quát',      'TAI_MUI_HONG'),
    ('BS. Phong Cloud',    'Phẫu thuật nội soi TMH',      'TAI_MUI_HONG'),
    ('BS. Quynh Meadow',   'Cơ xương khớp nội khoa',      'CO_XUONG_KHOP'),
    ('BS. Son Harbor',     'Thấp khớp học',               'CO_XUONG_KHOP'),
    ('BS. Thanh Willow',   'Chỉnh hình chấn thương',      'NGOAI_CHINH_HINH'),
    ('BS. Uy Summit',      'Phẫu thuật khớp và cột sống', 'NGOAI_CHINH_HINH')
) AS d(name, specialty, department_code)
WHERE o.slug = 'evergreen-demo'
ON CONFLICT DO NOTHING;

-- =============================================================================
-- Row-Level Security: tenant isolation
--
-- Every domain table is scoped by the `app.org_id` session variable, which
-- api-gateway resolves from the caller's verified claims and every backend
-- service sets with `SELECT set_config('app.org_id', '<uuid>', true)` as the
-- first statement of each request's transaction (see
-- src/agent.py::_set_org_context). Reading a table without that set first
-- raises an error (`''::uuid` fails to cast) rather than leaking every
-- tenant's rows — fail closed, not fail open.
--
-- `FORCE ROW LEVEL SECURITY` so the policy applies even to the table owner –
-- but this only matters for a non-superuser role: Postgres superusers and
-- any role with BYPASSRLS ignore row security entirely, FORCE or not. The
-- DATABASE_URL every service connects with MUST be a plain role
-- (NOSUPERUSER, NOBYPASSRLS) or none of this isolation is actually
-- enforced. Verified against pgvector/pgvector:pg16 locally: connecting as
-- `postgres` (superuser) silently returned every tenant's rows; connecting
-- as a NOSUPERUSER/NOBYPASSRLS role correctly isolated them. Supabase's
-- default `postgres` role is not a superuser, which is why this holds
-- there — don't assume the same of an arbitrary self-hosted Postgres.
-- =============================================================================

DO $$
DECLARE
    t TEXT;
BEGIN
    FOR t IN SELECT unnest(ARRAY[
        'departments', 'clinics', 'doctors', 'appointments',
        'triage_logs', 'human_triage_queue'
    ])
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format(
            'DROP POLICY IF EXISTS tenant_isolation ON %I', t
        );
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I
                USING (org_id = current_setting(''app.org_id'', true)::uuid)
                WITH CHECK (org_id = current_setting(''app.org_id'', true)::uuid)',
            t
        );
    END LOOP;
END $$;

-- red_flags: org_id IS NULL rows are the global default set, visible to every
-- tenant; org-specific rows (future extension) are scoped like everything else.
ALTER TABLE red_flags ENABLE ROW LEVEL SECURITY;
ALTER TABLE red_flags FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON red_flags;
CREATE POLICY tenant_isolation ON red_flags
    USING (org_id IS NULL OR org_id = current_setting('app.org_id', true)::uuid)
    WITH CHECK (org_id IS NULL OR org_id = current_setting('app.org_id', true)::uuid);
