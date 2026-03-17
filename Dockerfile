# Build stage
FROM golang:1.25.8 AS builder

WORKDIR /workspace

# Copy module files first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY controllers/ controllers/
COPY pkg/ pkg/

# Build the binary with CGO disabled for a statically-linked binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o manager ./cmd/

# Runtime stage: use distroless for minimal attack surface
FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=builder /workspace/manager .

USER 65532:65532

ENTRYPOINT ["/manager"]
