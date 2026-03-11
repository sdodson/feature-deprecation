# Build stage
FROM golang:1.23-bookworm AS builder

ARG GOARCH=amd64

WORKDIR /workspace

# Cache module downloads before copying source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOARCH=${GOARCH} go build \
    -ldflags="-s -w" \
    -o manager \
    ./cmd/feature-deprecation-controller/

# Runtime stage — distroless for minimal attack surface.
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /

COPY --from=builder /workspace/manager .

USER 65532:65532

ENTRYPOINT ["/manager"]
