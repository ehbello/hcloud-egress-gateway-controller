IMG ?= ghcr.io/maarlab-rethinking/hcloud-egress-gateway-controller:dev
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0

.PHONY: all tidy generate manifests build test docker fmt vet lint helm-lint
all: build

tidy:
	go mod tidy

generate: ## deepcopy
	$(CONTROLLER_GEN) object:headerFile=/dev/null paths="./api/..."

manifests: ## CRD -> chart
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:dir=charts/hcloud-egress-gateway-controller/crds

fmt:
	gofmt -w api cmd internal
vet:
	go vet ./...
lint:
	$(GOLANGCI_LINT) run ./...
helm-lint:
	helm lint charts/hcloud-egress-gateway-controller
	helm template heg charts/hcloud-egress-gateway-controller -n egress-system > /dev/null
build: fmt
	CGO_ENABLED=0 go build -o bin/manager ./cmd
test:
	go test ./...
docker:
	docker build -t $(IMG) .
