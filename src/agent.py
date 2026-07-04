"""
agent.py - Core AI pipeline for TriageOS.

Pipeline stages (in order):
1. De-identification  : Presidio strips PII/PHI before any LLM call
2. Symptom extraction : LLM extracts core symptoms from noisy free-text
3. Red-flag check     : pgvector cosine similarity → EMERGENCY if > 0.85
4. LLM triage         : Map symptoms → department + confidence score
5. Clinical summary   : Short nurse-facing summary
6. DB persistence     : Insert triage_log + queue entry when needed
"""

from __future__ import annotations

import json
import logging
import re
import uuid
from contextlib import asynccontextmanager
from typing import Any, AsyncGenerator, Literal

import httpx
import psycopg2
import psycopg2.extras
from openai import AsyncOpenAI
from openai.types.chat import ChatCompletionMessageParam

from .config import settings

logger = logging.getLogger("triageos.agent")

# ---------------------------------------------------------------------------
# AGENT CONFIGURATION (Prompt & Tools)
# ---------------------------------------------------------------------------

_AGENT_SYSTEM_PROMPT = """Bạn là trợ lý AI Điều dưỡng Sơ yếu của TriageOS cho Evergreen Clinic Network, một hệ thống phòng khám hư cấu dùng cho demo. Nhiệm vụ của bạn là thu thập triệu chứng và phân luồng bệnh nhân.

QUY TẮC PHẢN HỒI (CAO NHẤT)
1. CHÀO HỎI: Nếu bệnh nhân chỉ chào hỏi (ví dụ: "hello", "chào bạn"), hãy chào lại lịch sự và nhắc họ tiếp tục cung cấp triệu chứng hoặc vị trí.
2. KHÔNG KÊ ĐƠN: Nếu khách hàng rõ ràng yêu cầu kê thuốc hoặc hỏi về đơn thuốc, BẮT BUỘC trả lời: "Tôi là trợ lý AI phân khoa, không có thẩm quyền kê đơn thuốc."
3. KHÔNG TRẢ LỜI NGOÀI Y TẾ: Nếu khách hỏi vấn đề không liên quan (thời tiết, chứng khoán), hãy từ chối lịch sự và quay lại nhiệm vụ.
4. THIN HUMAN-TRIAGE (QUAN TRỌNG): Với các triệu chứng mơ hồ, không rõ ràng (uể oải, mệt mỏi, đau nhức chung chung), bạn KHÔNG ĐƯỢC tự ý chốt chuyên khoa. Hãy đặt Confidence < 60% và gọi tool `escalate_to_human_nurse`.
5. ƯU TIÊN TOOL: Luôn gọi `check_emergency` đầu tiên. Chỉ khi an toàn mới hỏi vị trí và thực hiện các bước tiếp theo.

Quy tắc hoạt động BẮT BUỘC (Agentic Loop):
BƯỚC 1 - QUÉT CẤP CỨU (ƯU TIÊN TỐI THƯỢNG): Ngay khi user nhắc đến bất kỳ triệu chứng nào mới, bạn PHẢI gọi tool `check_emergency` ĐẦU TIÊN. Tuyệt đối không phản hồi trước khi có kết quả từ tool này.
BƯỚC 2 - KHAI THÁC TRIỆU CHỨNG (FOLLOW-UP): Nếu triệu chứng quá chung chung (như "đau bụng", "đau đầu"), hãy đặt 1-2 câu hỏi ngắn gọn để làm rõ (VD: đau vùng nào, đau từ bao giờ, có sốt không).
BƯỚC 3 - LẤY VỊ TRÍ & PHÂN LUỒNG:
   - Khi đã thu thập đủ triệu chứng và Tự tin >= 85%: Bắt buộc phải biết bệnh nhân ĐANG Ở ĐÂU để gọi tool `resolve_and_get_booking_info`. Nếu chưa biết, hãy hỏi vị trí.
   - Khi triệu chứng vẫn mơ hồ, Tự tin < 85%: BẮT BUỘC gọi tool `escalate_to_human_nurse` (không cần hỏi vị trí).

Các cơ sở demo của Evergreen Clinic Network
- Midtown Clinic (100 Demo Care Way, Ba Dinh, Ha Noi)
- Riverside Clinic (200 Sample Health Street, Cau Giay, Ha Noi)
- Lakeside Clinic (300 Fictional Wellness Avenue, Long Bien, Ha Noi)

Danh sách CÁC CHUYÊN KHOA hợp lệ (BẮT BUỘC sử dụng mã chính xác trong department_code):
- TIM_MACH: Nội Tim Mạch (tim đập bất thường, đau ngực, huyết áp cao/thấp)
- NGOAI_TH: Ngoại Tiêu hoá (đau bụng, buồn nôn, nôn mửa, tiêu chảy, táo bón)
- THAN_KINH: Nội Thần Kinh (đau đầu, chóng mặt, mất ngủ)
- SAN_PHU: Sản Phụ Khoa (kinh nguyệt, thai sản, viêm phụ khoa)
- NHI: Nhi Khoa (trẻ em dưới 16 tuổi, sốt trẻ em, ho trẻ em)
- DA_LIEU: Da liễu (mẩn ngứa, nổi mề đay, mụn trứng cá, eczema)
- MAT: Nhãn Khoa (đau mắt, mờ mắt, đỏ mắt, chảy ghèn)
- TAI_MUI_HONG: Tai Mũi Họng (đau họng, viêm xoang, ù tai, chảy máu mũi)
- CO_XUONG_KHOP: Cơ Xương Khớp (đau lưng, đau khớp, thoái hóa khớp)
- NGOAI_CHINH_HINH: Ngoại Chỉnh hình (chấn thương xương, gãy xương)

Tuyệt đối không chẩn đoán bệnh hay kê đơn thuốc. Ưu tiên sử dụng tool để hoàn thành nhiệm vụ, hạn chế nói chuyện vòng vo."""
_AGENT_TOOLS: list[Any] = [
    {
        "type": "function",
        "function": {
            "name": "check_emergency",
            "description": "Kiểm tra các dấu hiệu cảnh báo đỏ (Cấp cứu). BẮT BUỘC gọi công cụ này ngay sau khi nhận được triệu chứng mới.",
            "parameters": {
                "type": "object",
                "properties": {
                    "symptoms": {
                        "type": "string",
                        "description": "Chỉ trích xuất TỪ KHÓA triệu chứng cốt lõi, cực kỳ ngắn gọn (VD: 'đau thắt ngực', 'khó thở', 'đột quỵ'). Không đưa cả câu dài.",
                    }
                },
                "required": ["symptoms"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "escalate_to_human_nurse",
            "description": "Chuyển ca bệnh cho điều dưỡng thật khi không chắc chắn về chuyên khoa (độ tự tin < 85%) hoặc cần chẩn đoán phức tạp.",
            "parameters": {
                "type": "object",
                "properties": {
                    "clinical_summary": {
                        "type": "string",
                        "description": "Tóm tắt bệnh án ngắn gọn cho điều dưỡng",
                    },
                    "suggested_dept": {
                        "type": "string",
                        "description": "Mã chuyên khoa dự đoán (có thể null)",
                    },
                },
                "required": ["clinical_summary"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "resolve_and_get_booking_info",
            "description": "Gọi khi ĐÃ CHẮC CHẮN (>85%) về chuyên khoa VÀ ĐÃ BIẾT VỊ TRÍ bệnh nhân. Lấy danh sách bác sĩ và cơ sở gần nhất để bệnh nhân đặt lịch.",
            "parameters": {
                "type": "object",
                "properties": {
                    "department_code": {
                        "type": "string",
                        "description": "Mã khoa (VD: TIM_MACH, NGOAI_TH, ...)",
                    },
                    "department_name": {
                        "type": "string",
                        "description": "Tên khoa bằng tiếng Việt",
                    },
                    "nearest_facility": {
                        "type": "string",
                        "description": "Suy luận địa lý để chọn ra 1 cơ sở gần vị trí bệnh nhân nhất (chọn đúng 1 trong: 'Midtown Clinic', 'Riverside Clinic', 'Lakeside Clinic'). NẾU CHƯA BIẾT VỊ TRÍ BỆNH NHÂN, để chuỗi rỗng '' và tool sẽ bị từ chối.",
                    },
                },
                "required": ["department_code", "department_name", "nearest_facility"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "book_appointment",
            "description": "Gọi để tự động đặt lịch khám cho bệnh nhân khi họ đã chọn được bác sĩ và thời gian khám.",
            "parameters": {
                "type": "object",
                "properties": {
                    "doctor_id": {
                        "type": "string",
                        "description": "UUID của bác sĩ bệnh nhân chọn",
                    },
                    "department_code": {
                        "type": "string",
                        "description": "Mã khoa khám (VD: NGOAI_TH, TIM_MACH...)",
                    },
                    "appointment_time": {
                        "type": "string",
                        "description": "Thời gian khám định dạng ISO 8601 (VD: '2026-04-10T08:00:00+07:00')",
                    },
                },
                "required": ["doctor_id", "department_code", "appointment_time"],
            },
        },
    },
]

# ---------------------------------------------------------------------------
# Module-level singletons (initialised lazily to avoid import-time crashes)
# ---------------------------------------------------------------------------

_openai_client: AsyncOpenAI | None = None


def _get_openai() -> AsyncOpenAI:
    """Return (or create) the shared AsyncOpenAI client."""
    global _openai_client
    if _openai_client is None:
        _openai_client = AsyncOpenAI(api_key=settings.OPENAI_API_KEY)
    return _openai_client


# ---------------------------------------------------------------------------
# 1. De-identification (regex-based, no external dependencies)
# ---------------------------------------------------------------------------

# Compiled patterns for common PII found in Vietnamese healthcare text
_PII_PATTERNS: list[tuple[re.Pattern[str], str]] = [
    # Vietnamese mobile numbers: 03x, 05x, 07x, 08x, 09x – both local (0) and
    # international (+84) prefixes.  The local form is 10 digits; the +84 form
    # drops the leading 0, yielding 11 chars total (e.g. +84912345678).
    (
        re.compile(r"(?:\+84|0)(3[2-9]|5[25689]|7[06-9]|8[0-9]|9[0-9])\d{7}"),
        "<SĐT>",
    ),
    # Vietnamese national ID / CCCD: exactly 9 or 12 digits (standalone)
    (re.compile(r"(?<!\d)\d{9}(?!\d)|(?<!\d)\d{12}(?!\d)"), "<CMND/CCCD>"),
    # Email addresses
    (re.compile(r"\b[\w.+-]+@[\w-]+\.[a-zA-Z]{2,}\b"), "<EMAIL>"),
    # IPv4 addresses
    (re.compile(r"\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b"), "<IP>"),
    # URLs (http / https)
    (re.compile(r"https?://\S+"), "<URL>"),
    # Vietnamese full names: 2-4 words (capitalised Unicode) preceded by
    # common identity phrases like "tên tôi là", "họ tên", "bệnh nhân".
    # The last word in Vietnamese names is often a single uppercase letter
    # (e.g. "Nguyễn Văn A"), so we allow single-char final tokens.
    # We replace the ENTIRE match (keyword + name) with <TÊN_BN>.
    (
        re.compile(
            r"(?:"
            r"tên(?:\s+(?:tôi|em|mình|bé|con|là))?|"
            r"họ\s+(?:và\s+)?tên|"
            r"bệnh\s+nhân|"
            r"tôi\s+là|em\s+là|mình\s+là"
            r")\s*:?\s*"
            r"(?:"
            # First word: must have at least 2 chars
            r"[A-ZÀÁÂÃÈÉÊÌÍÒÓÔÕÙÚÝĂĐƠƯẠẢẤẦẨẪẬẮẰẲẴẶẸẺẼẾỀỂỄỆỈỊỌỎỐỒỔỖỘỚỜỞỠỢỤỦỨỪỬỮỰỲỴỶỸ]"
            r"[a-zàáâãèéêìíòóôõùúýăđơưạảấầẩẫậắằẳẵặẹẻẽếềểễệỉịọỏốồổỗộớờởỡợụủứừửữựỳỵỷỹ]+"
            r"(?:\s+"
            # Subsequent words: 1+ chars (handles single-letter given names like "A")
            r"[A-ZÀÁÂÃÈÉÊÌÍÒÓÔÕÙÚÝĂĐƠƯẠẢẤẦẨẪẬẮẰẲẴẶẸẺẼẾỀỂỄỆỈỊỌỎỐỒỔỖỘỚỜỞỠỢỤỦỨỪỬỮỰỲỴỶỸ]"
            r"[a-zàáâãèéêìíòóôõùúýăđơưạảấầẩẫậắằẳẵặẹẻẽếềểễệỉịọỏốồổỗộớờởỡợụủứừửữựỳỵỷỹ]*"
            r"){1,3}"
            r")",
            re.UNICODE,
        ),
        "<TÊN_BN>",  # replace the full match (keyword + name) with placeholder
    ),
]


def deidentify_text(text: str) -> str:
    """
    Strip PII/PHI from *text* using compiled regex patterns before sending
    to the LLM cloud.

    Covers: Vietnamese phone numbers, national IDs (CCCD/CMND), email
    addresses, IP addresses, URLs, and names introduced by common Vietnamese
    phrases (e.g. "tên tôi là Nguyễn Văn A").

    Parameters
    ----------
    text:
        Raw free-text from the patient.

    Returns
    -------
    str
        Anonymised text with PII replaced by labelled placeholders.
    """
    if not text or not text.strip():
        return text

    for pattern, replacement in _PII_PATTERNS:
        try:
            text = pattern.sub(replacement, text)
        except Exception as exc:  # noqa: BLE001
            logger.warning("PII regex substitution failed: %s", exc)

    return text


# ---------------------------------------------------------------------------
# 2. Embeddings
# ---------------------------------------------------------------------------


async def get_embedding(text: str) -> list[float]:
    """
    Generate a 1 536-dimensional embedding for *text* using
    ``text-embedding-3-small``.

    Parameters
    ----------
    text:
        The input string to embed.

    Returns
    -------
    list[float]
        A list of 1 536 floats representing the embedding vector.
    """
    client = _get_openai()
    response = await client.embeddings.create(
        model=settings.OPENAI_EMBEDDING_MODEL,
        input=text.replace("\n", " "),
        dimensions=settings.OPENAI_EMBEDDING_DIMS,
    )
    return response.data[0].embedding


# ---------------------------------------------------------------------------
# 3. Red-flag check
# ---------------------------------------------------------------------------


# Fail-safe, not fail-open: CHECK_FAILED must never collapse into SAFE.
RedFlagStatus = Literal["EMERGENCY", "SAFE", "CHECK_FAILED"]


async def check_red_flags(
    symptoms_text: str,
    conn: Any,  # psycopg2 connection
) -> tuple[RedFlagStatus, str, float]:
    """
    Check whether *symptoms_text* semantically matches any red-flag keyword.

    Returns ``"EMERGENCY"`` if the top-1 cosine similarity against the
    ``red_flags`` table exceeds ``settings.RED_FLAG_SIMILARITY_THRESHOLD``,
    ``"SAFE"`` otherwise, or ``"CHECK_FAILED"`` if the check couldn't run at
    all (embedding/DB error, empty table) — callers must treat that the same
    as ``"EMERGENCY"``, see ``run_triage_pipeline``.
    """
    try:
        embedding = await get_embedding(symptoms_text)
        embedding_str = "[" + ",".join(str(x) for x in embedding) + "]"

        with conn.cursor(cursor_factory=psycopg2.extras.DictCursor) as cur:
            cur.execute(
                """
                SELECT keyword,
                       1 - (embedding <=> %s::vector) AS similarity
                FROM   red_flags
                ORDER  BY similarity DESC
                LIMIT  1
                """,
                (embedding_str,),
            )
            row = cur.fetchone()

        if row is None:
            logger.error(
                "red_flags table is empty – emergency check cannot run; "
                "failing safe."
            )
            return "CHECK_FAILED", "", 0.0

        keyword: str = row["keyword"]
        similarity: float = float(row["similarity"])

        logger.info(
            "Red-flag check: top match='%s' similarity=%.4f", keyword, similarity
        )

        if similarity >= settings.RED_FLAG_SIMILARITY_THRESHOLD:
            return "EMERGENCY", keyword, similarity

        return "SAFE", keyword, similarity

    except Exception as exc:  # noqa: BLE001
        logger.error("Red-flag check failed: %s", exc, exc_info=True)
        return "CHECK_FAILED", "", 0.0


# ---------------------------------------------------------------------------
# 4. LLM symptom extraction
# ---------------------------------------------------------------------------

_EXTRACTION_SYSTEM_PROMPT = """\
Bạn là trợ lý y tế. Nhiệm vụ DUY NHẤT của bạn là trích xuất các triệu chứng \
lâm sàng thuần túy từ đoạn văn bản của bệnh nhân.

Quy tắc:
- Loại bỏ tên, số điện thoại, địa chỉ và mọi thông tin nhận dạng cá nhân.
- Giữ lại thông tin về: triệu chứng, thời gian xuất hiện, mức độ nghiêm trọng, \
tuổi/giới tính nếu có.
- Trả về JSON duy nhất: \
{"symptoms": "mô tả triệu chứng ngắn gọn", "age": số hoặc null, "gender": "nam"/"nữ"/null}
- Không thêm bất kỳ văn bản nào ngoài JSON."""


async def extract_symptoms(raw_text: str) -> dict[str, Any]:
    """
    Use the LLM to extract structured symptom information from noisy free-text.

    Parameters
    ----------
    raw_text:
        De-identified patient input.

    Returns
    -------
    dict
        ``{"symptoms": str, "age": int | None, "gender": str | None}``
    """
    client = _get_openai()

    response = await client.chat.completions.create(
        model=settings.OPENAI_CHAT_MODEL,
        messages=[
            {"role": "system", "content": _EXTRACTION_SYSTEM_PROMPT},
            {"role": "user", "content": raw_text},
        ],
        # temperature=0.1,
        max_tokens=300,
        response_format={"type": "json_object"},
    )

    content = response.choices[0].message.content or "{}"
    try:
        parsed = json.loads(content)
    except json.JSONDecodeError:
        logger.warning("Symptom extraction: JSON parse failed; returning raw text.")
        parsed = {"symptoms": raw_text, "age": None, "gender": None}

    # Ensure all expected keys are present
    parsed.setdefault("symptoms", raw_text)
    parsed.setdefault("age", None)
    parsed.setdefault("gender", None)

    return parsed


# ---------------------------------------------------------------------------
# 5. LLM triage routing
# ---------------------------------------------------------------------------


async def triage_symptoms(
    symptoms_text: str,
    conversation_history: list[dict[str, str]] | None = None,
) -> dict[str, Any]:
    """
    Call the LLM triage router to map symptoms to a department.

    Parameters
    ----------
    symptoms_text:
        Core symptom description (already extracted and de-identified).
    conversation_history:
        Optional prior turns in the session for multi-turn context.

    Returns
    -------
    dict
        Parsed JSON from the LLM with keys:
        ``department_code``, ``department_name``, ``confidence_score``,
        ``follow_up_question`` (may be ``None``), ``clinical_summary``.
    """
    client = _get_openai()

    messages: list[ChatCompletionMessageParam] = [
        {"role": "system", "content": settings.TRIAGE_SYSTEM_PROMPT},
    ]

    # Inject previous conversation turns for multi-turn awareness
    if conversation_history:
        for turn in conversation_history:
            role = turn.get("role", "user")
            content = turn.get("content", "")
            if role in ("user", "assistant", "system"):
                messages.append({"role": role, "content": content})  # type: ignore[arg-type]

    messages.append({"role": "user", "content": symptoms_text})

    response = await client.chat.completions.create(
        model=settings.OPENAI_CHAT_MODEL,
        messages=messages,
        temperature=0.2,  # Low temperature for consistent routing
        max_tokens=512,
        response_format={"type": "json_object"},
    )

    content = response.choices[0].message.content or "{}"

    try:
        result = json.loads(content)
    except json.JSONDecodeError as exc:
        logger.error("Triage LLM returned invalid JSON: %s | raw=%s", exc, content)
        # Graceful degradation: send to human queue
        result = {
            "department_code": None,
            "department_name": None,
            "confidence_score": 0,
            "follow_up_question": "Xin lỗi, hệ thống gặp sự cố. Điều dưỡng sẽ hỗ trợ bạn.",
            "clinical_summary": f"Lỗi phân tích tự động. Triệu chứng gốc: {symptoms_text[:200]}",
        }

    # Normalise types
    try:
        raw_confidence = result.get("confidence_score", 0)
        result["confidence_score"] = (
            int(raw_confidence) if raw_confidence is not None else 0
        )
    except (TypeError, ValueError):
        result["confidence_score"] = 0

    result.setdefault("department_code", None)
    result.setdefault("department_name", None)
    result.setdefault("follow_up_question", None)
    result.setdefault("clinical_summary", "")

    return result


# ---------------------------------------------------------------------------
# 6. Clinical summary generation
# ---------------------------------------------------------------------------


async def generate_clinical_summary(
    symptoms: str,
    triage_result: dict[str, Any],
    age: int | None = None,
    gender: str | None = None,
) -> str:
    """
    Generate a concise clinical summary in Vietnamese for the nurse dashboard.

    This is a lightweight LLM call (≤ 120 tokens) so it adds minimal latency.

    Parameters
    ----------
    symptoms:
        Core symptom description.
    triage_result:
        The JSON dict returned by :func:`triage_symptoms`.
    age:
        Patient age (optional).
    gender:
        Patient gender string (optional).

    Returns
    -------
    str
        2–3 sentence Vietnamese clinical summary.
    """
    # If the LLM already provided a summary, use it directly
    existing_summary: str = triage_result.get("clinical_summary", "").strip()
    if existing_summary and len(existing_summary) > 20:
        return existing_summary

    client = _get_openai()

    demographics = ""
    if age:
        demographics += f"Tuổi: {age}. "
    if gender:
        demographics += f"Giới tính: {gender}. "

    prompt = (
        f"{demographics}Triệu chứng: {symptoms}\n"
        f"Khoa đề xuất: {triage_result.get('department_name', 'Chưa xác định')} "
        f"(confidence: {triage_result.get('confidence_score', 0)}%).\n\n"
        "Viết tóm tắt lâm sàng ngắn gọn (2-3 câu) bằng tiếng Việt cho điều dưỡng. "
        "Không chẩn đoán, không kê đơn. Chỉ mô tả triệu chứng và lý do điều phối."
    )

    try:
        response = await client.chat.completions.create(
            model=settings.OPENAI_CHAT_MODEL,
            messages=[{"role": "user", "content": prompt}],
            temperature=0.3,
            max_tokens=150,
        )
        return (response.choices[0].message.content or "").strip()
    except Exception as exc:  # noqa: BLE001
        logger.warning("Clinical summary generation failed: %s", exc)
        dept = triage_result.get("department_name", "chưa xác định")
        return (
            f"Bệnh nhân có triệu chứng: {symptoms[:120]}. "
            f"AI đề xuất điều phối đến {dept} "
            f"(độ tin cậy: {triage_result.get('confidence_score', 0)}%)."
        )


# ---------------------------------------------------------------------------
# 7. Database helpers
# ---------------------------------------------------------------------------


def _get_db_connection() -> Any:
    """
    Open a new psycopg2 connection using ``settings.DATABASE_URL``.

    Uses ``psycopg2.extras.register_uuid()`` so UUID objects are handled
    natively and ``register_default_jsonb`` for any JSONB columns.
    """
    psycopg2.extras.register_uuid()
    conn = psycopg2.connect(settings.DATABASE_URL)
    conn.autocommit = False
    return conn


def _set_org_context(conn: Any, org_id: str) -> None:
    """
    Bind every subsequent statement on *conn*'s transaction to *org_id* via
    the ``app.org_id`` Postgres session variable that every table's
    row-level-security policy checks (see db/init.sql). This is the
    tenant-isolation enforcement point — callers never need a ``WHERE
    org_id = ...`` app-layer filter.

    Uses ``set_config(..., is_local=true)`` rather than ``SET LOCAL`` because
    psycopg2 can't bind a parameter into a ``SET`` statement; ``set_config``
    is a normal function call and accepts one like any other query.
    """
    with conn.cursor() as cur:
        cur.execute("SELECT set_config('app.org_id', %s, true)", (str(org_id),))


@asynccontextmanager
async def db_connection(org_id: str) -> AsyncGenerator[Any, None]:
    """
    Async context manager that yields a psycopg2 connection scoped to
    *org_id* and handles commit / rollback / close automatically.

    Usage::

        async with db_connection(ctx.org_id) as conn:
            do_something(conn)
    """
    conn = None
    try:
        conn = _get_db_connection()
        _set_org_context(conn, org_id)
        yield conn
        conn.commit()
    except Exception:
        if conn is not None:
            conn.rollback()
        raise
    finally:
        if conn is not None:
            conn.close()


async def insert_triage_log(
    conn: Any,
    raw_symptoms: str,
    symptom_embedding: list[float],
    ai_suggested_dept: str | None,
    confidence: float | None,
) -> str:
    """
    Insert a row into ``triage_logs`` and return the new UUID string.

    The ``final_dept`` and ``resolution_type`` columns are left NULL here;
    they are filled in later when a nurse resolves the linked
    ``human_triage_queue`` entry (queue-service, Phase 3 — see
    ``create_queue_item`` below for how the two rows get linked via
    ``triage_log_id``).
    """
    log_id = str(uuid.uuid4())
    embedding_str = "[" + ",".join(str(x) for x in symptom_embedding) + "]"

    with conn.cursor() as cur:
        cur.execute(
            """
            INSERT INTO triage_logs
                (id, org_id, raw_symptoms, symptom_embedding, ai_suggested_dept, confidence)
            VALUES (%s, current_setting('app.org_id')::uuid, %s, %s::vector, %s, %s)
            """,
            (log_id, raw_symptoms, embedding_str, ai_suggested_dept, confidence),
        )
    return log_id


async def create_queue_item(
    org_id: str,
    patient_session_id: str,
    clinical_summary: str,
    suggested_dept: str | None,
    triage_log_id: str | None,
) -> str:
    """
    Create a ``human_triage_queue`` entry via queue-service's internal API.

    queue-service (Go) owns human_triage_queue as of Phase 3, so this is a
    server-to-server call authenticated the same way api-gateway
    authenticates to identity-service (a shared internal secret) — never
    routed through api-gateway itself, which only forwards patient/staff
    traffic, not internal service-to-service calls.

    Passing *triage_log_id* is what lets queue-service's resolve handler
    back-fill the exact originating ``triage_logs`` row later instead of
    guessing by department match (see docs/architecture/implementation-plan.md
    §4 and services/queue/db.go::resolveQueueItem).
    """
    async with httpx.AsyncClient(timeout=5.0) as client:
        response = await client.post(
            f"{settings.QUEUE_SERVICE_URL}/internal/queue/items",
            json={
                "org_id": org_id,
                "patient_session_id": patient_session_id,
                "clinical_summary": clinical_summary,
                "suggested_dept": suggested_dept,
                "triage_log_id": triage_log_id,
            },
            headers={"X-Internal-Secret": settings.INTERNAL_SHARED_SECRET},
        )
        response.raise_for_status()
        queue_id: str = response.json()["queue_id"]

    logger.info("Created queue entry %s for patient %s", queue_id, patient_session_id)
    return queue_id


async def seed_red_flags(conn: Any, keywords: list[str]) -> int:
    """
    Generate OpenAI embeddings for each keyword and upsert into ``red_flags``.

    Uses ``INSERT … ON CONFLICT (keyword) DO UPDATE`` so re-running the
    endpoint is idempotent.

    Returns
    -------
    int
        Number of rows inserted/updated.
    """
    count = 0
    for keyword in keywords:
        try:
            embedding = await get_embedding(keyword)
            embedding_str = "[" + ",".join(str(x) for x in embedding) + "]"

            with conn.cursor() as cur:
                cur.execute(
                    """
                    INSERT INTO red_flags (keyword, embedding)
                    VALUES (%s, %s::vector)
                    ON CONFLICT (keyword)
                    DO UPDATE SET embedding = EXCLUDED.embedding
                    """,
                    (keyword, embedding_str),
                )
            count += 1
            logger.info("Seeded red-flag keyword: '%s'", keyword)
        except Exception as exc:  # noqa: BLE001
            logger.error("Failed to seed red-flag '%s': %s", keyword, exc)

    conn.commit()
    return count


# ---------------------------------------------------------------------------
# 7b. Doctor / clinic / appointment helpers
# ---------------------------------------------------------------------------


async def get_doctors_by_department(
    org_id: str,
    department_code: str,
) -> list[dict[str, Any]]:
    """
    Fetch up to 5 doctors for *department_code* via scheduling-service's
    internal API (Phase 4 — departments/doctors/clinics/appointments moved
    out of this monolith into services/scheduling).

    Returns
    -------
    list[dict]
        Each dict has keys: ``id``, ``name``, ``specialty``, ``department_code``.
    """
    try:
        async with httpx.AsyncClient(timeout=5.0) as client:
            response = await client.get(
                f"{settings.SCHEDULING_SERVICE_URL}/internal/scheduling/doctors",
                params={"org_id": org_id, "department_code": department_code},
                headers={"X-Internal-Secret": settings.INTERNAL_SHARED_SECRET},
            )
            response.raise_for_status()
            doctors: list[dict[str, Any]] = response.json()["doctors"]
            return doctors
    except Exception as exc:  # noqa: BLE001
        logger.warning("get_doctors_by_department failed: %s", exc)
        return []


async def get_clinics_by_department(
    org_id: str,
    department_code: str,
) -> list[dict[str, Any]]:
    """
    Fetch all clinics serving *department_code* via scheduling-service's
    internal API (Phase 4).

    Returns
    -------
    list[dict]
        Each dict has keys ``name``, ``address``.
    """
    try:
        async with httpx.AsyncClient(timeout=5.0) as client:
            response = await client.get(
                f"{settings.SCHEDULING_SERVICE_URL}/internal/scheduling/clinics",
                params={"org_id": org_id, "department_code": department_code},
                headers={"X-Internal-Secret": settings.INTERNAL_SHARED_SECRET},
            )
            response.raise_for_status()
            clinics: list[dict[str, Any]] = response.json()["clinics"]
            return clinics
    except Exception as exc:  # noqa: BLE001
        logger.warning("get_clinics_by_department failed: %s", exc)
        return []


async def create_appointment(
    org_id: str,
    patient_session_id: str,
    doctor_id: str,
    department_code: str,
    appointment_time: str,
) -> str:
    """
    Book an appointment via scheduling-service's internal API (Phase 4).

    Called from the ``book_appointment`` chat tool — a server-to-server
    call authenticated the same way as ``create_queue_item``, never routed
    through api-gateway. No ``Idempotency-Key`` here: the agentic loop only
    invokes this tool once per booking decision, unlike the patient-facing
    ``POST /api/v1/appointments`` route (which api-gateway now routes
    directly to scheduling-service, see services/gateway/main.go), where a
    client retry is the exact case that header exists to make safe.

    Parameters
    ----------
    appointment_time:
        ISO 8601 string (e.g. ``"2026-04-10T08:00:00+07:00"``).
    """
    async with httpx.AsyncClient(timeout=5.0) as client:
        response = await client.post(
            f"{settings.SCHEDULING_SERVICE_URL}/internal/scheduling/appointments",
            json={
                "org_id": org_id,
                "patient_session_id": patient_session_id,
                "doctor_id": doctor_id,
                "department_code": department_code,
                "appointment_time": appointment_time,
            },
            headers={"X-Internal-Secret": settings.INTERNAL_SHARED_SECRET},
        )
        response.raise_for_status()
        appt_id: str = response.json()["appointment_id"]

    logger.info(
        "Appointment created: id=%s patient=%s doctor=%s",
        appt_id,
        patient_session_id,
        doctor_id,
    )
    return appt_id


# ---------------------------------------------------------------------------
# 8. Main orchestration pipeline
# ---------------------------------------------------------------------------


async def run_triage_pipeline(
    patient_id: str,
    org_id: str,
    message: str,
    conversation_history: list[dict[str, str]] | None = None,
) -> dict[str, Any]:

    clean_text = deidentify_text(message)
    client = _get_openai()

    messages: list[ChatCompletionMessageParam] = [
        {"role": "system", "content": _AGENT_SYSTEM_PROMPT}
    ]

    if conversation_history:
        for turn in conversation_history:
            # Only add standard roles
            if turn.get("role") in ["user", "assistant", "system", "tool"]:
                messages.append(
                    {"role": turn.get("role"), "content": turn.get("content", "")}
                )  # type: ignore

    messages.append({"role": "user", "content": clean_text})

    conn = None
    try:
        conn = _get_db_connection()
        _set_org_context(conn, org_id)
    except Exception as exc:
        logger.warning("DB unavailable (%s); proceeding with caution.", exc)

    result: dict[str, Any] = {
        "flow": "FOLLOW_UP",
        "department_code": None,
        "department_name": None,
        "confidence_score": None,
        "patient_message": None,
        "queue_id": None,
        "doctors": None,
        "clinics": None,
    }

    # Vòng lặp tự trị (Tối đa 3 steps để tránh infinite loop)
    MAX_ITERATIONS = 5
    for _ in range(MAX_ITERATIONS):
        response = await client.chat.completions.create(
            model=settings.OPENAI_CHAT_MODEL,
            messages=messages,
            tools=_AGENT_TOOLS,
            tool_choice="auto",
            temperature=0.1,
        )

        response_message = response.choices[0].message
        messages.append(response_message)  # type: ignore

        if not response_message.tool_calls:
            # Agent quyết định giao tiếp trực tiếp với user (Follow-up)
            result["flow"] = "FOLLOW_UP"
            result["patient_message"] = response_message.content
            break

        # Agent quyết định sử dụng Tool
        tool_calls = response_message.tool_calls
        for tool_call in tool_calls:
            function_name = tool_call.function.name
            args = json.loads(tool_call.function.arguments)

            tool_result = ""

            if function_name == "check_emergency":
                if conn:
                    check_status, keyword, sim = await check_red_flags(
                        args["symptoms"], conn
                    )
                else:
                    check_status, keyword, sim = "CHECK_FAILED", "", 0.0

                if check_status in ("EMERGENCY", "CHECK_FAILED"):
                    if check_status == "CHECK_FAILED":
                        logger.error(
                            "Emergency check failed for org=%s patient=%s — failing safe",
                            org_id,
                            patient_id,
                        )
                    result["flow"] = "EMERGENCY"
                    result["matched_keyword"] = (
                        keyword
                        if check_status == "EMERGENCY"
                        else "Không thể xác minh mức độ khẩn cấp – vui lòng gọi 115 ngay"
                    )
                    result["similarity_score"] = sim
                    return result  # Ngắt ngay lập tức
                tool_result = "No emergency detected. Safe to proceed."

            elif function_name == "escalate_to_human_nurse":
                result["flow"] = "PENDING_HUMAN"
                if conn:
                    triage_log_id = None
                    try:
                        symptom_embedding = await get_embedding(clean_text)
                        triage_log_id = await insert_triage_log(
                            conn,
                            raw_symptoms=clean_text,
                            symptom_embedding=symptom_embedding,
                            ai_suggested_dept=args.get("suggested_dept"),
                            confidence=None,  # unknown — that's why it escalated
                        )
                        conn.commit()
                    except Exception as exc:  # noqa: BLE001
                        # Best-effort: the flywheel log is valuable but not
                        # worth failing the escalation over if it errors.
                        conn.rollback()
                        logger.error(
                            "Failed to log triage_logs row before escalation: %s",
                            exc,
                            exc_info=True,
                        )

                    result["queue_id"] = await create_queue_item(
                        org_id=org_id,
                        patient_session_id=patient_id,
                        clinical_summary=args["clinical_summary"],
                        suggested_dept=args.get("suggested_dept"),
                        triage_log_id=triage_log_id,
                    )
                tool_result = "Escalated successfully."
                result["patient_message"] = (
                    "Hệ thống đã ghi nhận triệu chứng. Tôi đang chuyển hồ sơ của bạn cho điều dưỡng chuyên môn để hỗ trợ trực tiếp."
                )
                return result
            elif function_name == "book_appointment":
                try:
                    await create_appointment(
                        org_id=org_id,
                        patient_session_id=patient_id,
                        doctor_id=args["doctor_id"],
                        department_code=args["department_code"],
                        appointment_time=args["appointment_time"],
                    )
                    result["flow"] = "AUTO_RESOLVED"
                    result["patient_message"] = (
                        f"✅ Lịch hẹn của bạn đã được đặt thành công vào lúc {args['appointment_time']}. "
                        "Mã đặt lịch của bạn là hệ thống đã ghi nhận. Xin vui lòng đến đúng giờ và mang theo giấy tờ tùy thân nhé!"
                    )
                    return result
                except Exception as exc:  # noqa: BLE001
                    logger.error(
                        "Failed to book appointment via scheduling-service: %s",
                        exc,
                        exc_info=True,
                    )
                    tool_result = (
                        "Booking failed. Tell the patient booking is temporarily "
                        "unavailable and to try again shortly."
                    )

            elif function_name == "resolve_and_get_booking_info":
                nearest_facility = args.get("nearest_facility", "").strip()
                if not nearest_facility or nearest_facility.lower() in [
                    "chưa rõ",
                    "không rõ",
                    "unknown",
                    "thành phố",
                    "hà nội",
                ]:
                    tool_result = "ERROR: Missing nearest_facility. You must ask the patient to specify their current district/area before calling this tool."
                    messages.append(
                        {
                            "role": "tool",
                            "tool_call_id": tool_call.id,
                            "name": function_name,
                            "content": tool_result,
                        }
                    )
                    continue

                result["flow"] = "AUTO_RESOLVED"
                result["department_code"] = args["department_code"]
                result["department_name"] = args["department_name"]
                result["doctors"] = await get_doctors_by_department(
                    org_id, args["department_code"]
                )
                all_clinics = await get_clinics_by_department(
                    org_id, args["department_code"]
                )
                # Sort clinics: matching nearest facility name to top
                if nearest_facility:
                    loc_lower = nearest_facility.lower()
                    # Xử lý các keyword từ LLM để map với DB
                    if "times" in loc_lower:
                        loc_lower = "times"
                    elif "royal" in loc_lower:
                        loc_lower = "royal"
                    elif "ocean" in loc_lower:
                        loc_lower = "ocean"

                    def _clinic_sort_key(c: dict) -> int:
                        name_lower = c.get("name", "").lower()
                        if loc_lower in name_lower:
                            return 0  # match → top
                        return 1

                    all_clinics.sort(key=_clinic_sort_key)
                result["clinics"] = all_clinics
                tool_result = "Booking info retrieved."

                nearest_clinic_name = ""
                nearest_clinic_address = ""
                if result.get("clinics"):
                    nearest_clinic_name = result["clinics"][0].get("name", "")
                    nearest_clinic_address = result["clinics"][0].get("address", "")

                patient_loc_text = (
                    f" Dựa trên vị trí của bạn, gần nhất là cơ sở {nearest_clinic_name} (Tại địa chỉ: {nearest_clinic_address})."
                    if nearest_clinic_name
                    else ""
                )

                result["patient_message"] = (
                    f"Tôi khuyên bạn nên khám tại khoa {args['department_name']}."
                    + patient_loc_text
                    + " Dưới đây là các bác sĩ và cơ sở phù hợp đễ bạn đặt lịch khám!"
                )
                return result

            # Feed kết quả tool lại cho Agent
            messages.append(
                {
                    "role": "tool",
                    "tool_call_id": tool_call.id,
                    "name": function_name,
                    "content": tool_result,
                }
            )

    if conn:
        conn.close()

    return result
