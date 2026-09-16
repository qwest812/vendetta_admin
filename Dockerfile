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
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 app
COPY --from=build /out/server /usr/local/bin/server

USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]
