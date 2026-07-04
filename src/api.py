"""
api.py - FastAPI application for TriageOS.

Endpoints
---------
GET  /                              – Root info
GET  /health                        – Health check (DB ping + system metrics)
POST /api/v1/chat/triage            – Main patient triage chat endpoint
POST /api/v1/admin/seed-red-flags   – Seed red-flag embeddings into DB (one-time)

Nurse-queue endpoints (GET /api/v1/queue/pending, POST /api/v1/queue/resolve,
POST /api/v1/queue/check-timeouts) moved to queue-service (Go) in Phase 3, and
POST /api/v1/appointments moved to scheduling-service (Go) in Phase 4 —
api-gateway routes all of them there directly now, this backend no longer
serves any of them at all (see services/queue, services/scheduling, and
services/gateway/main.go).
"""

from __future__ import annotations

import logging
import time
import uuid
from contextlib import asynccontextmanager
from typing import Any

import psutil
import psycopg2
from fastapi import Depends, FastAPI, HTTPException, Request, status
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from langfuse import get_client, observe
from slowapi import Limiter, _rate_limit_exceeded_handler
from slowapi.errors import RateLimitExceeded
from slowapi.util import get_remote_address

from .agent import (
    db_connection,
    run_triage_pipeline,
    seed_red_flags,
)
from .config import settings
from .context import PatientContext, StaffContext, get_patient_context, require_roles
from .schema import (
    ChatRequest,
    ChatResponse,
    ClinicInfo,
    DoctorInfo,
    EmergencyResult,
    ErrorResponse,
    SeedRedFlagsResponse,
    TriageFlow,
    TriageResult,
)

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s  %(levelname)-8s  %(name)s  %(message)s",
    datefmt="%Y-%m-%dT%H:%M:%S",
)
logger = logging.getLogger("triageos.api")

# ---------------------------------------------------------------------------
# Rate limiter
# ---------------------------------------------------------------------------

limiter = Limiter(key_func=get_remote_address, default_limits=["200/minute"])

# ---------------------------------------------------------------------------
# App startup / shutdown lifecycle
# ---------------------------------------------------------------------------

START_TIME = time.time()


@asynccontextmanager
async def lifespan(app: FastAPI):  # noqa: ARG001
    """
    Application lifespan handler.

    On startup: log configuration summary.
    On shutdown: flush Langfuse traces.
    """
    logger.info("=== TriageOS API starting ===")
    logger.info("Chat model  : %s", settings.OPENAI_CHAT_MODEL)
    logger.info("Embed model : %s", settings.OPENAI_EMBEDDING_MODEL)
    logger.info("CORS origins: %s", settings.CORS_ORIGINS)
    logger.info("Red-flag threshold : %.2f", settings.RED_FLAG_SIMILARITY_THRESHOLD)
    logger.info(
        "Human-triage threshold : %d", settings.HUMAN_TRIAGE_CONFIDENCE_THRESHOLD
    )
    yield
    # Flush any pending Langfuse events before the process exits
    try:
        lf = get_client()
        lf.flush()
        logger.info("Langfuse traces flushed.")
    except Exception:  # noqa: BLE001
        pass
    logger.info("=== TriageOS API stopped ===")


# ---------------------------------------------------------------------------
# FastAPI app
# ---------------------------------------------------------------------------

app = FastAPI(
    title="TriageOS API",
    description=(
        "AI-powered patient triage system for a fictional clinic network demo. "
        "De-identifies PII, detects red-flag emergencies via semantic similarity, "
        "routes symptoms to the appropriate department, and queues low-confidence "
        "cases for nurse review. Independent portfolio project; not affiliated "
        "with any real hospital."
    ),
    version="1.0.0",
    docs_url="/docs",
    redoc_url="/redoc",
    lifespan=lifespan,
)

# ---------------------------------------------------------------------------
# Middleware
# ---------------------------------------------------------------------------

# CORS – allow the Next.js frontend
app.add_middleware(
    CORSMiddleware,
    allow_origins=settings.CORS_ORIGINS,
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

# Rate limiting
app.state.limiter = limiter
app.add_exception_handler(RateLimitExceeded, _rate_limit_exceeded_handler)


@app.middleware("http")
async def add_process_time_header(request: Request, call_next):
    """Attach X-Process-Time header and emit a structured access-log line."""
    start = time.time()
    response = await call_next(request)
    elapsed = time.time() - start
    response.headers["X-Process-Time"] = f"{elapsed:.4f}"
    logger.info(
        "path=%s method=%s status=%d latency=%.3fs",
        request.url.path,
        request.method,
        response.status_code,
        elapsed,
    )
    return response


# ---------------------------------------------------------------------------
# Exception handlers
# ---------------------------------------------------------------------------


@app.exception_handler(HTTPException)
async def http_exception_handler(request: Request, exc: HTTPException):  # noqa: ARG001
    return JSONResponse(
        status_code=exc.status_code,
        content=ErrorResponse(
            code="HTTP_ERROR",
            message=exc.detail,
        ).model_dump(),
    )


@app.exception_handler(Exception)
async def unhandled_exception_handler(request: Request, exc: Exception):  # noqa: ARG001
    logger.error("Unhandled exception: %s", exc, exc_info=True)
    return JSONResponse(
        status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
        content=ErrorResponse(
            code="INTERNAL_SERVER_ERROR",
            message="Đã xảy ra lỗi nội bộ. Vui lòng thử lại sau.",
        ).model_dump(),
    )


# ---------------------------------------------------------------------------
# Root & health
# ---------------------------------------------------------------------------


@app.get("/", tags=["Meta"])
def read_root():
    """API root – basic info."""
    return {
        "service": "TriageOS API",
        "version": "1.0.0",
        "docs": "/docs",
        "health": "/health",
    }


@app.get("/health", tags=["Meta"])
def health_check():
    """
    Liveness / readiness probe.

    Pings the database and reports system resource usage.
    """
    uptime = time.time() - START_TIME
    ram = psutil.virtual_memory().percent
    cpu = psutil.cpu_percent(interval=None)

    db_status = "disconnected"
    try:
        with psycopg2.connect(settings.DATABASE_URL) as conn:
            with conn.cursor() as cur:
                cur.execute("SELECT 1")
        db_status = "connected"
    except Exception as exc:  # noqa: BLE001
        db_status = f"error: {exc}"

    return {
        "status": "healthy",
        "database": db_status,
        "uptime_seconds": round(uptime, 2),
        "system": {
            "ram_percent": ram,
            "cpu_percent": cpu,
        },
        "config": {
            "chat_model": settings.OPENAI_CHAT_MODEL,
            "embedding_model": settings.OPENAI_EMBEDDING_MODEL,
            "red_flag_threshold": settings.RED_FLAG_SIMILARITY_THRESHOLD,
            "human_triage_threshold": settings.HUMAN_TRIAGE_CONFIDENCE_THRESHOLD,
        },
    }


# ---------------------------------------------------------------------------
# Helper: build patient-facing message
# ---------------------------------------------------------------------------


def _build_patient_message(flow: str, triage: dict[str, Any]) -> str:
    """
    Compose a patient-facing message string from the pipeline result dict.
    """
    dept_name: str = triage.get("department_name") or "chuyên khoa phù hợp"
    follow_up: str | None = triage.get("follow_up_question")
    patient_message: str | None = triage.get("patient_message")

    if flow == "FOLLOW_UP":
        return (
            patient_message
            or follow_up
            or "Bạn có thể mô tả chi tiết hơn về các triệu chứng được không?"
        )

    if flow == "PENDING_HUMAN":
        base = patient_message or (
            "Các triệu chứng của bạn cần được đánh giá chi tiết hơn. "
            "Tôi đã chuyển thông tin của bạn đến điều dưỡng chuyên môn để hỗ trợ trực tiếp."
        )
        if follow_up and base != follow_up:
            # If the LLM didn't naturally include the follow up in patient_message, append it.
            if follow_up not in base:
                base += f"\n\n🩺 Trong lúc chờ đợi, {follow_up}"
        return base

    # AUTO_RESOLVED
    return patient_message or (
        f"Dựa trên các triệu chứng bạn vừa mô tả, tôi khuyên bạn nên đến khám tại chuyên khoa {dept_name}. "
        "Bạn vui lòng chọn cơ sở và đặt lịch khám với bác sĩ ở bên dưới nhé."
    )


# ---------------------------------------------------------------------------
# POST /api/v1/chat/triage
# ---------------------------------------------------------------------------


@app.post(
    "/api/v1/chat/triage",
    response_model=ChatResponse,
    status_code=status.HTTP_200_OK,
    tags=["Triage"],
    summary="Patient triage chat",
    responses={
        200: {
            "description": "Triage result (AUTO_RESOLVED / PENDING_HUMAN / EMERGENCY)"
        },
        429: {"description": "Rate limit exceeded"},
        500: {"description": "Internal server error"},
    },
)
@limiter.limit(settings.RATE_LIMIT_CHAT)
@observe()
async def chat_triage(
    request: Request,  # required by slowapi limiter
    body: ChatRequest,
    ctx: PatientContext = Depends(get_patient_context),
):
    """
    Main patient-facing triage endpoint.

    Pipeline
    --------
    1. De-identify PII with Presidio.
    2. Extract core symptoms via LLM.
    3. Check semantic similarity against red-flag embeddings (pgvector).
       - If similarity > 0.85 → return EMERGENCY immediately.
    4. LLM routing: map symptoms to department + confidence score.
    5. Generate short clinical summary for nurse dashboard.
    6. Persist triage_log + human_triage_queue (if confidence < 85).

    Returns ``flow = AUTO_RESOLVED | PENDING_HUMAN | EMERGENCY``.
    """
    # Attach Langfuse trace metadata
    try:
        langfuse = get_client()
        langfuse.update_current_trace(
            session_id=body.session_id or str(uuid.uuid4()),
            user_id=ctx.patient_session_id,
            tags=["triageos", "v1"],
            metadata={
                "org_id": ctx.org_id,
                "message_length": len(body.message),
                "has_history": bool(body.conversation_history),
            },
        )
    except Exception:  # noqa: BLE001
        pass  # Langfuse is optional – never block the request

    logger.info(
        "Triage request: org=%s patient_session=%s session=%s msg_len=%d",
        ctx.org_id,
        ctx.patient_session_id,
        body.session_id,
        len(body.message),
    )

    pipeline_result = await run_triage_pipeline(
        patient_id=ctx.patient_session_id,
        org_id=ctx.org_id,
        message=body.message,
        conversation_history=body.conversation_history or [],
    )

    flow_str: str = pipeline_result.get("flow", "PENDING_HUMAN")
    flow = TriageFlow(flow_str)

    # ------------------------------------------------------------------
    # EMERGENCY path
    # ------------------------------------------------------------------
    if flow == TriageFlow.EMERGENCY:
        emergency = EmergencyResult(
            matched_keyword=pipeline_result.get("matched_keyword") or "unknown",
            similarity_score=float(pipeline_result.get("similarity_score") or 0.0),
        )
        logger.warning(
            "EMERGENCY: org=%s patient_session=%s keyword='%s' score=%.4f",
            ctx.org_id,
            ctx.patient_session_id,
            emergency.matched_keyword,
            emergency.similarity_score,
        )
        return ChatResponse(
            status="success",
            flow=flow,
            emergency=emergency,
        )

    # ------------------------------------------------------------------
    # AUTO_RESOLVED / PENDING_HUMAN / FOLLOW_UP path
    # ------------------------------------------------------------------
    patient_msg = _build_patient_message(flow_str, pipeline_result)

    # Build doctor / clinic info for AUTO_RESOLVED
    doctors = None
    clinics = None
    if flow == TriageFlow.AUTO_RESOLVED:
        raw_doctors = pipeline_result.get("doctors") or []
        doctors = [
            DoctorInfo(
                id=d["id"],
                name=d["name"],
                specialty=d["specialty"],
                department_code=d["department_code"],
            )
            for d in raw_doctors
        ]
        raw_clinics = pipeline_result.get("clinics") or []
        clinics = [
            ClinicInfo(name=c["name"], address=c["address"]) for c in raw_clinics
        ]

    triage_result = TriageResult(
        department_code=pipeline_result.get("department_code"),
        department_name=pipeline_result.get("department_name"),
        confidence_score=pipeline_result.get("confidence_score"),
        message=patient_msg,
        follow_up_question=pipeline_result.get("follow_up_question"),
        queue_id=pipeline_result.get("queue_id"),
        clinical_summary=pipeline_result.get("clinical_summary"),
        doctors=doctors,
        clinics=clinics,
    )

    logger.info(
        "Triage done: patient_session=%s flow=%s dept=%s confidence=%s queue_id=%s",
        ctx.patient_session_id,
        flow_str,
        triage_result.department_code,
        triage_result.confidence_score,
        triage_result.queue_id,
    )

    return ChatResponse(
        status="success",
        flow=flow,
        result=triage_result,
    )


# ---------------------------------------------------------------------------
# POST /api/v1/admin/seed-red-flags
# ---------------------------------------------------------------------------


@app.post(
    "/api/v1/admin/seed-red-flags",
    response_model=SeedRedFlagsResponse,
    status_code=status.HTTP_200_OK,
    tags=["Admin"],
    summary="Seed red-flag emergency keyword embeddings into DB",
    responses={
        200: {"description": "Red-flag keywords seeded successfully"},
        503: {"description": "Database or OpenAI unavailable"},
    },
)
@limiter.limit(settings.RATE_LIMIT_ADMIN)
async def seed_red_flags_endpoint(
    request: Request,  # noqa: ARG001
    ctx: StaffContext = Depends(require_roles("ADMIN", "OWNER")),
):
    """
    Generate OpenAI embeddings for all 15 Vietnamese emergency red-flag
    keywords and upsert them into the ``red_flags`` table.

    This endpoint is **idempotent** – running it multiple times is safe;
    existing rows are updated with fresh embeddings via
    ``ON CONFLICT (keyword) DO UPDATE``.

    **When to call:**
    - Once after the initial DB migration.
    - After changing the embedding model to regenerate all vectors.

    Restricted to ADMIN/OWNER — api-gateway already enforces this at the
    routing layer, this is defense-in-depth. The seeded keywords are global
    defaults (``org_id IS NULL``, shared by every tenant); per-org overrides
    are a later extension, not required by this phase.
    """
    keywords = settings.RED_FLAG_KEYWORDS

    try:
        async with db_connection(ctx.org_id) as conn:
            inserted = await seed_red_flags(conn, keywords)
    except Exception as exc:  # noqa: BLE001
        logger.error("Red-flag seeding failed: %s", exc, exc_info=True)
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail=f"Không thể seed red flags: {exc}",
        ) from exc

    logger.info("Red-flag seeding complete: %d/%d keywords", inserted, len(keywords))

    return SeedRedFlagsResponse(
        success=True,
        inserted=inserted,
        keywords=keywords,
        message=(
            f"Đã seed thành công {inserted}/{len(keywords)} từ khóa nguy hiểm "
            f"vào bảng red_flags."
        ),
    )

