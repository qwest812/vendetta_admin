.PHONY: up down restart logs ps dev build test tidy clean

# Поднять всё в docker: postgres и приложение.
up:
	docker compose up -d --build
	@echo "Админка: http://localhost:$${APP_PORT:-8080}"

down:
	docker compose down

# Пересобрать образ приложения и перезапустить его, не трогая базу.
restart:
	docker compose up -d --build app

logs:
	docker compose logs -f app

ps:
	docker compose ps

# Локальная разработка: база в docker, приложение из исходников.
dev:
	docker compose up -d postgres
	set -a; . ./.env; set +a; go run ./cmd/server

build:
	go build -o bin/vendetta ./cmd/server

test:
	go test ./...

tidy:
	go mod tidy

# Снести контейнеры вместе с данными базы.
clean:
	docker compose down -v
