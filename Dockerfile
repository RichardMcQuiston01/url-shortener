# syntax=docker/dockerfile:1

# ---- build: a static binary, no cgo ----
FROM golang:1.26-alpine AS build
# Use the image's own Go; never download a toolchain during a build.
ENV GOTOOLCHAIN=local
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
# An empty /data owned by the non-root user, so the app can create its database.
RUN mkdir -p /out/data && touch /out/data/.keep

# ---- run: minimal image, non-root ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
COPY --from=build --chown=nonroot:nonroot /out/data /data

VOLUME /data
USER nonroot:nonroot
EXPOSE 8080

# The image has no shell, curl or wget, so the binary checks itself.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/server", "healthcheck"]

ENTRYPOINT ["/server"]
