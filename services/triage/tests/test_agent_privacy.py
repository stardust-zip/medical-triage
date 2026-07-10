from triage.agent import deidentify_text


def test_deidentify_text_removes_common_patient_identifiers():
    raw = (
        "Tên tôi là Nguyễn Văn A, số điện thoại 0912345678, "
        "email patient@example.com, CCCD 012345678901."
    )

    clean = deidentify_text(raw)

    assert "Nguyễn Văn A" not in clean
    assert "0912345678" not in clean
    assert "patient@example.com" not in clean
    assert "012345678901" not in clean
    assert "<TÊN_BN>" in clean
    assert "<SĐT>" in clean
    assert "<EMAIL>" in clean
    assert "<CMND/CCCD>" in clean
