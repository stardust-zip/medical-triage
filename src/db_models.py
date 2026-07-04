from __future__ import annotations

import uuid

from sqlalchemy import (
    CheckConstraint,
    DateTime,
    Enum,
    Float,
    ForeignKey,
    ForeignKeyConstraint,
    Index,
    String,
    Text,
    UniqueConstraint,
    func,
)
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column
from sqlalchemy.types import UserDefinedType


class Vector(UserDefinedType):
    cache_ok = True

    def __init__(self, dimensions: int) -> None:
        self.dimensions = dimensions

    def get_col_spec(self, **_kw: object) -> str:
        return f"VECTOR({self.dimensions})"


class Base(DeclarativeBase):
    pass


class Organization(Base):
    __tablename__ = "organizations"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    slug: Mapped[str] = mapped_column(String(100), unique=True, nullable=False)
    plan_tier: Mapped[str] = mapped_column(String(50), nullable=False, default="demo")
    created_at: Mapped[DateTime] = mapped_column(
        DateTime(timezone=True), server_default=func.now()
    )


user_role = Enum("OWNER", "ADMIN", "NURSE", "DOCTOR", name="user_role")
queue_status = Enum("PENDING", "RESOLVED", "TIMEOUT", name="queue_status")
triage_resolution = Enum(
    "AI_AUTO",
    "NURSE_APPROVED",
    "NURSE_CORRECTED",
    "DOCTOR_CORRECTED",
    name="triage_resolution",
)


class User(Base):
    __tablename__ = "users"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True)
    org_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("organizations.id", ondelete="CASCADE")
    )
    auth_user_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), unique=True, nullable=False
    )
    email: Mapped[str] = mapped_column(String(255), nullable=False)
    role: Mapped[str] = mapped_column(user_role, nullable=False, default="NURSE")
    created_at: Mapped[DateTime] = mapped_column(
        DateTime(timezone=True), server_default=func.now()
    )

    __table_args__ = (Index("idx_users_org", "org_id"),)


class Department(Base):
    __tablename__ = "departments"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True)
    org_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("organizations.id", ondelete="CASCADE")
    )
    code: Mapped[str] = mapped_column(String(50), nullable=False)
    name: Mapped[str] = mapped_column(String(255), nullable=False)

    __table_args__ = (UniqueConstraint("org_id", "code"),)


class TriageLog(Base):
    __tablename__ = "triage_logs"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True)
    org_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("organizations.id", ondelete="CASCADE")
    )
    raw_symptoms: Mapped[str] = mapped_column(Text, nullable=False)
    symptom_embedding: Mapped[list[float] | None] = mapped_column(Vector(1536))
    ai_suggested_dept: Mapped[str | None] = mapped_column(String(255))
    confidence: Mapped[float | None] = mapped_column(Float)
    final_dept: Mapped[str | None] = mapped_column(String(255))
    resolution_type: Mapped[str | None] = mapped_column(triage_resolution)
    created_at: Mapped[DateTime] = mapped_column(
        DateTime(timezone=True), server_default=func.now()
    )

    __table_args__ = (Index("idx_triage_logs_org", "org_id"),)


class HumanTriageQueue(Base):
    __tablename__ = "human_triage_queue"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True)
    org_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("organizations.id", ondelete="CASCADE")
    )
    patient_id: Mapped[str] = mapped_column(String(255), nullable=False)
    clinical_summary: Mapped[str] = mapped_column(Text, nullable=False)
    suggested_dept: Mapped[str | None] = mapped_column(String(255))
    triage_log_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True), ForeignKey("triage_logs.id", ondelete="SET NULL")
    )
    status: Mapped[str] = mapped_column(queue_status, default="PENDING")
    created_at: Mapped[DateTime] = mapped_column(
        DateTime(timezone=True), server_default=func.now()
    )

    __table_args__ = (
        Index("idx_human_triage_queue_status", "status", "created_at"),
        Index("idx_human_triage_queue_org", "org_id"),
        Index("idx_human_triage_queue_triage_log_id", "triage_log_id"),
    )


class RedFlag(Base):
    __tablename__ = "red_flags"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True)
    org_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True), ForeignKey("organizations.id", ondelete="CASCADE")
    )
    keyword: Mapped[str] = mapped_column(Text, unique=True, nullable=False)
    embedding: Mapped[list[float] | None] = mapped_column(Vector(1536))


class Clinic(Base):
    __tablename__ = "clinics"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True)
    org_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("organizations.id", ondelete="CASCADE")
    )
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    address: Mapped[str] = mapped_column(Text, nullable=False)
    department_code: Mapped[str] = mapped_column(String(50), nullable=False)

    __table_args__ = (
        ForeignKeyConstraint(
            ["org_id", "department_code"],
            ["departments.org_id", "departments.code"],
            ondelete="CASCADE",
        ),
        Index("idx_clinics_dept", "org_id", "department_code"),
    )


class Doctor(Base):
    __tablename__ = "doctors"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True)
    org_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("organizations.id", ondelete="CASCADE")
    )
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    specialty: Mapped[str] = mapped_column(String(255), nullable=False)
    department_code: Mapped[str] = mapped_column(String(50), nullable=False)

    __table_args__ = (
        ForeignKeyConstraint(
            ["org_id", "department_code"],
            ["departments.org_id", "departments.code"],
            ondelete="CASCADE",
        ),
        UniqueConstraint("org_id", "id"),
        Index("idx_doctors_dept", "org_id", "department_code"),
    )


class Appointment(Base):
    __tablename__ = "appointments"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True)
    org_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("organizations.id", ondelete="CASCADE")
    )
    patient_id: Mapped[str] = mapped_column(String(255), nullable=False)
    doctor_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    department_code: Mapped[str] = mapped_column(String(50), nullable=False)
    appointment_time: Mapped[DateTime] = mapped_column(
        DateTime(timezone=True), nullable=False
    )
    idempotency_key: Mapped[str | None] = mapped_column(String(255))
    created_at: Mapped[DateTime] = mapped_column(
        DateTime(timezone=True), server_default=func.now()
    )

    __table_args__ = (
        ForeignKeyConstraint(
            ["org_id", "doctor_id"], ["doctors.org_id", "doctors.id"], ondelete="CASCADE"
        ),
        Index("idx_appointments_org", "org_id"),
        CheckConstraint("appointment_time IS NOT NULL"),
        # Phase 4: double-booking prevention + idempotent booking. The
        # idempotency index is partial (WHERE idempotency_key IS NOT NULL)
        # in db/init.sql/the migration — SQLAlchemy's Index doesn't need
        # postgresql_where here since compare_type/autogenerate isn't used
        # for these raw-SQL-driven migrations, but the columns still need
        # to exist on the model for it to be a faithful mirror of the DB.
        Index(
            "idx_appointments_no_double_booking",
            "org_id",
            "doctor_id",
            "appointment_time",
            unique=True,
        ),
    )
