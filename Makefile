IMG ?= ghcr.io/maarlab-rethinking/hcloud-egress-gateway-controller:dev
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.23.0
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
CRD_REF_DOCS ?= go run github.com/elastic/crd-ref-docs@v0.3.0

CHART := charts/hcloud-egress-gateway-controller
GENDIR := .gen

.PHONY: all tidy generate manifests api-docs build test e2e-up e2e e2e-down docker fmt vet lint helm-lint
all: build

tidy:
	go mod tidy

generate: ## deepcopy
	$(CONTROLLER_GEN) object:headerFile=/dev/null paths="./api/..."

manifests: ## CRD + RBAC ClusterRole -> chart templates (generated; do not hand-edit)
	@rm -rf $(GENDIR) && mkdir -p $(GENDIR)
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:dir=$(GENDIR)
	$(CONTROLLER_GEN) rbac:roleName=manager-role paths="./internal/..." output:rbac:dir=$(GENDIR)
	@test -s $(GENDIR)/egress.maarlab.dev_hetzneregressgateways.yaml || { \
	  echo 'make manifests: controller-gen produced no CRD. Check the markers in api/.' >&2; exit 1; }
	@# controller-gen collects +kubebuilder:rbac only from package-level comments; markers
	@# on a declaration's doc comment are parsed as declaration markers, ignored, and it
	@# exits 0 having written nothing. Without this guard the block below would wrap an
	@# EMPTY ClusterRole and deploy the operator with no permissions at all -- a runtime
	@# "forbidden" that CI never catches once the empty file is committed (no drift).
	@test -s $(GENDIR)/role.yaml || { \
	  echo 'make manifests: controller-gen produced no role.yaml. The +kubebuilder:rbac markers must' >&2; \
	  echo 'be a floating comment block, separated from the declaration below by a blank line.' >&2; \
	  exit 1; }
	@{ \
	  echo '{{- /* Generated from api/v1alpha1 by "make manifests"; do not edit. */ -}}'; \
	  echo '{{- if .Values.installCRD }}'; \
	  cat $(GENDIR)/egress.maarlab.dev_hetzneregressgateways.yaml; \
	  echo '{{- end }}'; \
	} > $(CHART)/templates/crd.yaml
	@{ \
	  echo '{{- /* Generated from +kubebuilder:rbac markers by "make manifests"; do not edit. */ -}}'; \
	  echo '{{- if .Values.rbac.create }}'; \
	  sed -e '/^  creationTimestamp: null$$/d' \
	      -e 's|^  name: manager-role$$|  name: {{ .Release.Name }}|' \
	      $(GENDIR)/role.yaml; \
	  echo '{{- end }}'; \
	} > $(CHART)/templates/clusterrole.yaml
	@rm -rf $(GENDIR)

api-docs: ## API reference (docs/api.md) <- api types (generated; do not hand-edit)
	$(CRD_REF_DOCS) --source-path=./api/v1alpha1 --config=hack/crd-ref-docs.yaml --renderer=markdown --output-path=docs/api.md

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

# Cilium datapath e2e on kind (ADR-0010). Needs docker, kind, helm, kubectl.
CILIUM_VERSION ?= 1.20.3
e2e-up: ## kind cluster + Cilium $(CILIUM_VERSION) + upstream + simulated gateways
	CILIUM_VERSION=$(CILIUM_VERSION) ./hack/e2e/up.sh
e2e: ## run the e2e against the environment e2e-up brought up
	go test -tags e2e -count=1 -v ./test/e2e/
e2e-down:
	./hack/e2e/down.sh
docker:
	docker build -t $(IMG) .
