import os


os.environ.setdefault("OPENAI_API_KEY", "test-openai-key")
os.environ.setdefault("DATABASE_URL", "postgresql://triageos_app:test@localhost:5432/triageos")
os.environ.setdefault("GATEWAY_SHARED_SECRET", "test-gateway-secret")
os.environ.setdefault("INTERNAL_SHARED_SECRET", "test-internal-secret")
os.environ.setdefault("SUPABASE_JWT_SECRET", "test-supabase-secret")
os.environ.setdefault("PATIENT_SESSION_SECRET", "test-patient-secret")
