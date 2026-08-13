# syntax=docker/dockerfile:1
FROM golang:1.23 AS build
WORKDIR /src
# Download modules first (cached layer) — fail fast if the module set is broken.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o /out/manager ./cmd

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/manager /manager
# The agent mode needs NET_ADMIN + hostNetwork (set on the StatefulSet the controller
# generates) and runs privileged; the controller mode runs as nonroot by default.
USER 65532:65532
ENTRYPOINT ["/manager"]
