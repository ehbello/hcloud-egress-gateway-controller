# syntax=docker/dockerfile:1
FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/manager ./cmd

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/manager /manager
# The agent mode needs CAP_NET_ADMIN + hostNetwork (set on the StatefulSet the
# controller generates), not root; runs as nonroot by default.
USER 65532:65532
ENTRYPOINT ["/manager"]
