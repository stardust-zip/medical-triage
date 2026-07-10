.PHONY: bootstrap up down logs build migrate seed-staff goose-version test test-python test-go test-frontend ci ps

bootstrap:
	./setup.sh

up:
	docker compose up -d

down:
	docker compose down

logs:
	docker compose logs -f

build:
	docker compose build

migrate:
	docker compose run --rm migrate

seed-staff:
	docker compose run --rm -e IDENTITY_URL=http://identity:8082 api python scripts/seed_demo_staff.py

goose-version:
	docker build -t triageos-goose ./tools/goose
	docker run --rm -v $(PWD)/db/goose:/migrations triageos-goose --version

test: test-python test-go test-frontend

test-python:
	pytest

test-go:
	cd services/gateway && go test ./...
	cd services/identity && go test ./...
	cd services/queue && go test ./...
	cd services/scheduling && go test ./...

test-frontend:
	cd frontend && npm ci && npm audit --audit-level=moderate && npm run lint && npm run build

ci: test
	docker compose config
	docker compose build

ps:
	docker compose ps
