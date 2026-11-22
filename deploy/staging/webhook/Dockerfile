# syntax=docker/dockerfile:1
# Staging-only webhook capture sink. Base images are the same digests as the main Dockerfile.
FROM golang:1.27-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/webhook .

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/webhook /webhook
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/webhook"]
