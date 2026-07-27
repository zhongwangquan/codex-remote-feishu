FROM golang:1.24 AS builder

WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/codex-preview-relay ./cmd/codex-preview-relay

FROM debian:bookworm-slim

RUN useradd -r -s /usr/sbin/nologin relay
COPY --from=builder /out/codex-preview-relay /usr/local/bin/codex-preview-relay
USER relay
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/codex-preview-relay"]
