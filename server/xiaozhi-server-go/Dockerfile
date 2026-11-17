# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS builder
RUN apk add --no-cache gcc musl-dev pkgconf opus-dev opusfile-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o /out/xiaozhi-server ./src

FROM alpine:3.20
RUN apk add --no-cache ca-certificates sqlite-libs opus opusfile gettext tzdata
WORKDIR /app
COPY --from=builder /out/xiaozhi-server /app/xiaozhi-server
COPY web /app/web
COPY music /app/music
COPY config.yaml /app/config.yaml
CMD ["/app/xiaozhi-server"]
