# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src

# Слой с зависимостями кэшируется отдельно: правка кода его не инвалидирует.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
# Шаблоны, статика и миграции вшиты через embed, поэтому на выходе один
# статический бинарник без внешних файлов.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/vendetta ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 vendetta
COPY --from=build /out/vendetta /usr/local/bin/vendetta

USER vendetta
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/vendetta"]
