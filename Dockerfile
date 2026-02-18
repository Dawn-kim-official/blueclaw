FROM golang:1.25-bookworm AS builder
RUN apt-get update && apt-get install -y --no-install-recommends libsqlite3-dev && rm -rf /var/lib/apt/lists/*
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o /blueclaw ./cmd/blueclaw

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    wget \
    python3-minimal \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /blueclaw /usr/local/bin/blueclaw
ENTRYPOINT ["sleep", "infinity"]
