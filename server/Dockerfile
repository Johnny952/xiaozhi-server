# syntax=docker/dockerfile:1
# Build context: server/ — so the config template + entrypoint are baked into
# the image (Coolify cannot bind-mount repo files from the host).
FROM golang:1.24-alpine AS builder
RUN apk add --no-cache gcc musl-dev pkgconf opus-dev opusfile-dev
WORKDIR /src
COPY xiaozhi-server-go/go.mod xiaozhi-server-go/go.sum ./
RUN go mod download
COPY xiaozhi-server-go/ .
RUN CGO_ENABLED=1 go build -o /out/xiaozhi-server ./src

FROM golang:1.24-alpine AS mcpbuilder
WORKDIR /mcp
COPY nextcloud-mcp/go.mod nextcloud-mcp/go.sum ./
RUN go mod download
COPY nextcloud-mcp/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nextcloud-mcp .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates sqlite-libs opus opusfile gettext tzdata
WORKDIR /app
COPY --from=builder /out/xiaozhi-server /app/xiaozhi-server
COPY --from=mcpbuilder /out/nextcloud-mcp /app/nextcloud-mcp
COPY xiaozhi-server-go/web /app/web
COPY xiaozhi-server-go/music /app/music
COPY xiaozhi-server-go/config.yaml /app/config.yaml
COPY --chmod=644 xiaozhi/.mcp_server_settings.json /app/.mcp_server_settings.json
COPY --chmod=755 xiaozhi/entrypoint.sh /config/entrypoint.sh
COPY --chmod=644 xiaozhi/config.template.yaml /config/config.template.yaml
ENTRYPOINT ["/config/entrypoint.sh"]
