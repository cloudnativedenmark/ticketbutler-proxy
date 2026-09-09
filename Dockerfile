# syntax=docker/dockerfile:1

# Cloud Run runs linux/amd64 only, and the laptops this gets built on are arm64
# Macs. So the target architecture is pinned rather than inherited: the build stage
# runs natively on the host (BUILDPLATFORM) and Go cross-compiles to TARGETOS and
# TARGETARCH. Building without --platform on an arm64 machine produces an image
# Cloud Run refuses to start, and the error it gives is not obvious.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build

ARG TARGETOS
ARG TARGETARCH

# Stamped into main.version so a running revision can be traced back to a commit.
ARG VERSION=dev

WORKDIR /src

# Manifests first: dependencies change much less often than source, so the module
# download stays in the layer cache across source edits.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off because distroless/static carries no libc. -trimpath keeps absolute build
# paths out of the binary, so the same source yields the same bytes wherever it is
# built, which is what makes the provenance attestation worth having.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/tbproxy ./cmd/tbproxy

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.source="https://github.com/cloudnativedenmark/ticketbutler-proxy" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.description="Caching proxy that serves aggregated TicketButler order data to Google Apps Script"

COPY --from=build /out/tbproxy /tbproxy

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/tbproxy"]
CMD ["serve"]
