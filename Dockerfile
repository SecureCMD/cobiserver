# syntax=docker/dockerfile:1

FROM golang:1.23-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/koserver ./cmd/koserver

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
	&& addgroup -S koserver \
	&& adduser -S -G koserver koserver \
	&& mkdir -p /data \
	&& chown koserver:koserver /data

COPY --from=builder /out/koserver /usr/local/bin/koserver

USER koserver
ENV DATA_DIR=/data \
	LISTEN_ADDR=:8080
VOLUME ["/data"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
	CMD ["/bin/sh", "-c", "wget -q -O- http://127.0.0.1${LISTEN_ADDR:-:8080}/healthcheck > /dev/null || exit 1"]

ENTRYPOINT ["/usr/local/bin/koserver"]
CMD ["serve"]
