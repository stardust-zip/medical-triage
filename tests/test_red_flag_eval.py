"""Red-flag recall/precision eval against the real embedding pipeline.
Requires a real OPENAI_API_KEY (skipped otherwise — see _eval_support.py);
CI runs this in the `redflag-eval` job."""

from __future__ import annotations

import asyncio

import psycopg2
import pytest

from src.agent import check_red_flags, seed_red_flags
from src.config import settings

from _eval_support import requires_real_openai_key

pytestmark = requires_real_openai_key

# Paraphrases of the 15 seeded keywords, not verbatim (tests semantic match).
POSITIVE_CASES = [
    ("tự nhiên đau nhói ngực trái như bị bóp nghẹt", "đau thắt ngực"),
    ("ngực đau dữ dội, vã mồ hôi lạnh, khó thở", "nhồi máu cơ tim"),
    ("nói ngọng đột ngột, méo miệng, yếu nửa người", "đột quỵ"),
    ("tay chân một bên bỗng không cử động được", "liệt nửa người"),
    ("thở hổn hển, tím tái, không thở nổi", "khó thở nặng"),
    ("đau đầu dữ dội chưa từng có kèm nôn vọt", "xuất huyết não"),
    ("lên cơn co giật toàn thân, sùi bọt mép", "co giật"),
    ("gọi hoài không tỉnh, lay không phản ứng", "mất ý thức"),
    ("tim ngừng đập, không bắt được mạch", "ngừng tim"),
    ("thở rất nhanh và nông, môi tím tái", "suy hô hấp"),
    ("máu phun thành tia từ vết thương ở tay", "vỡ động mạch"),
    ("ngã đập đầu xuống đất, chảy máu tai", "chấn thương đầu nặng"),
    ("nổi mề đay toàn thân, sưng môi, khó thở sau khi ăn tôm", "sốc phản vệ"),
    ("ra huyết ồ ạt sau sinh, choáng váng", "băng huyết sau sinh"),
    ("ngủ li bì gọi mãi không dậy, thở yếu", "hôn mê"),
]

# Benign symptoms that must NOT trip the red-flag threshold.
NEGATIVE_CASES = [
    "hắt hơi sổ mũi nhẹ hai ngày nay",
    "đau bụng âm ỉ sau khi ăn đồ lạnh",
    "ngứa da nhẹ ở cánh tay, không sốt",
    "mỏi mắt sau khi làm việc máy tính cả ngày",
    "đau lưng nhẹ khi ngồi lâu",
    "ho khan nhẹ, không sốt, không khó thở",
    "đau răng khi ăn đồ ngọt",
    "mất ngủ nhẹ do stress công việc",
    "chóng mặt thoáng qua khi đứng dậy nhanh",
    "táo bón nhẹ vài ngày nay",
]


@pytest.fixture(scope="module")
def seeded_conn():
    """A real DB connection with the red_flags table freshly seeded."""
    conn = psycopg2.connect(settings.DATABASE_URL)
    try:
        asyncio.run(seed_red_flags(conn, settings.RED_FLAG_KEYWORDS))
        yield conn
    finally:
        conn.close()


def test_red_flag_recall_meets_target(seeded_conn):
    misses = [
        (text, expected_keyword, *asyncio.run(check_red_flags(text, seeded_conn)))
        for text, expected_keyword in POSITIVE_CASES
    ]
    misses = [m for m in misses if m[2] != "EMERGENCY"]

    recall = 1 - len(misses) / len(POSITIVE_CASES)
    # N=15 means 99.5% recall requires zero misses.
    assert recall >= 0.995, f"Recall {recall:.1%} below target; misses: {misses}"


def test_red_flag_precision_on_benign_negatives(seeded_conn):
    false_positives = [
        (text, *asyncio.run(check_red_flags(text, seeded_conn)))
        for text in NEGATIVE_CASES
    ]
    false_positives = [fp for fp in false_positives if fp[1] == "EMERGENCY"]

    precision_rate = 1 - len(false_positives) / len(NEGATIVE_CASES)
    assert precision_rate >= 0.9, (
        f"Too many false positives on benign symptoms: {false_positives}"
    )
