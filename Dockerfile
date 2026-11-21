# syntax=docker/dockerfile:1
# Gunkly: one static binary with the SPA embedded, on distroless (no shell).
# Base images are pinned by digest; Dependabot keeps them current.

# --- Front end ---------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN npm ci --ignore-scripts --no-audit
COPY web/ ./
RUN npm run build

# --- Go binary -----------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-mod=readonly
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" -o /out/gunkly ./cmd/gunkly \
 && mkdir -p /out/data/tmp

# --- Runtime -------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/gunkly /gunkly
# Seeds a fresh named volume with nonroot-owned /data and /data/tmp.
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
ENV SQLITE_TMPDIR=/data/tmp
EXPOSE 8080
VOLUME /data
# Distroless has no shell or curl: the binary probes its own /healthz.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/gunkly", "healthcheck"]
ENTRYPOINT ["/gunkly"]
CMD ["serve"]
