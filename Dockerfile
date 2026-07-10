FROM python:3.13-slim

# Shared migration tooling image (alembic + db_models.py + scripts/). The
# triage app itself lives in services/triage/Dockerfile.

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app

COPY pyproject.toml README.md ./
COPY db_models.py ./
RUN pip install --no-cache-dir .
COPY . .

RUN useradd --create-home --shell /usr/sbin/nologin appuser
USER appuser

CMD ["python", "scripts/migrate.py"]
