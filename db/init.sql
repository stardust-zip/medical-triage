-- =============================================================================
-- TriageOS - Database Initialisation Script
-- Independent portfolio demo. Seed data is fictional and synthetic.
-- Apply via psql or a hosted PostgreSQL SQL editor.
-- =============================================================================

-- ---------------------------------------------------------------------------
-- Extensions
-- ---------------------------------------------------------------------------

-- pgvector: enables VECTOR column type and cosine-similarity operator (<=>)
CREATE EXTENSION IF NOT EXISTS vector;

-- ---------------------------------------------------------------------------
-- ENUM types
-- ---------------------------------------------------------------------------

CREATE TYPE triage_resolution AS ENUM (
    'AI_AUTO',
    'NURSE_APPROVED',
    'NURSE_CORRECTED',
    'DOCTOR_CORRECTED'
);

CREATE TYPE queue_status AS ENUM (
    'PENDING',
    'RESOLVED',
    'TIMEOUT'
);

-- ---------------------------------------------------------------------------
-- Table: departments
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS departments (
    id   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(50)  UNIQUE NOT NULL,
    name VARCHAR(255) NOT NULL
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

-- ---------------------------------------------------------------------------
-- Table: human_triage_queue
--
-- Low-confidence cases (confidence < 85) are inserted here.
-- Nurses see these on their dashboard and approve or correct the routing.
-- SLA: items older than 3 minutes without resolution are marked TIMEOUT.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS human_triage_queue (
    id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id       VARCHAR(255) NOT NULL,
    clinical_summary TEXT         NOT NULL,
    suggested_dept   VARCHAR(255),
    status           queue_status DEFAULT 'PENDING',
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Index for fast lookup of PENDING items (nurse dashboard polling)
CREATE INDEX IF NOT EXISTS idx_human_triage_queue_status
    ON human_triage_queue (status, created_at ASC);

-- ---------------------------------------------------------------------------
-- Table: red_flags
--
-- Stores Vietnamese emergency keyword embeddings for semantic red-flag detection.
--
-- HOW TO POPULATE:
--   Embeddings cannot be pre-inserted in plain SQL because they must be
--   generated at runtime via the OpenAI Embeddings API.
--
--   After running this migration, call the seed endpoint once:
--
--       POST /api/v1/admin/seed-red-flags
--
--   That endpoint will:
--     1. Iterate over all 15 Vietnamese emergency keywords defined in config.py.
--     2. Call OpenAI text-embedding-3-small to generate a 1536-dim vector for each.
--     3. Upsert each row via ON CONFLICT (keyword) DO UPDATE – so it is safe
--        to call multiple times (e.g. after switching embedding models).
--
-- Emergency keywords that will be seeded:
--   đau thắt ngực, nhồi máu cơ tim, đột quỵ, liệt nửa người, khó thở nặng,
--   xuất huyết não, co giật, mất ý thức, ngừng tim, suy hô hấp,
--   vỡ động mạch, chấn thương đầu nặng, sốc phản vệ, băng huyết sau sinh, hôn mê
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS red_flags (
    id        UUID  PRIMARY KEY DEFAULT gen_random_uuid(),
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

INSERT INTO departments (code, name) VALUES
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
ON CONFLICT (code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Table: clinics
-- Fictional demo clinics used for "nearest clinic" lookup.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS clinics (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    address         TEXT         NOT NULL,
    department_code VARCHAR(50)  NOT NULL REFERENCES departments(code) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_clinics_dept ON clinics (department_code);

-- ---------------------------------------------------------------------------
-- Table: doctors
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS doctors (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    specialty       VARCHAR(255) NOT NULL,
    department_code VARCHAR(50)  NOT NULL REFERENCES departments(code) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_doctors_dept ON doctors (department_code);

-- ---------------------------------------------------------------------------
-- Table: appointments
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS appointments (
    id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id       VARCHAR(255) NOT NULL,
    doctor_id        UUID         NOT NULL REFERENCES doctors(id) ON DELETE CASCADE,
    department_code  VARCHAR(50)  NOT NULL,
    appointment_time TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- Seed: clinics (one per department)
-- ---------------------------------------------------------------------------

INSERT INTO clinics (name, address, department_code) VALUES
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
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Seed: doctors (~2 per department)
-- ---------------------------------------------------------------------------

INSERT INTO doctors (name, specialty, department_code) VALUES
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
ON CONFLICT DO NOTHING;
