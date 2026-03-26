# mixin.mk — reusable make targets for jsonnet monitoring mixins.
#
# Include this file from any project Makefile:
#
#   include build/mixin.mk
#
# Override these variables before the include line if the defaults don't fit:
#
#   MIXIN_DIR         ?= monitoring
#   MIXIN_RENDER_FILE ?= render.jsonnet
#   MIXIN_OUTPUT      ?= config/prometheus/rules.yaml
#   JSONNET_VERSION   ?= v0.20.0
#   JB_VERSION        ?= v0.5.1

##@ Monitoring

## Tool versions (jsonnet and jsonnetfmt share the same go-jsonnet release)
JSONNET_VERSION ?= v0.20.0
JB_VERSION      ?= v0.5.1

## Local bin directory — reuse $(LOCALBIN) if already defined by the parent Makefile.
LOCALBIN ?= $(shell pwd)/bin

## Tool binary paths
JSONNET    ?= $(LOCALBIN)/jsonnet-$(JSONNET_VERSION)
JSONNETFMT ?= $(LOCALBIN)/jsonnetfmt-$(JSONNET_VERSION)
JB         ?= $(LOCALBIN)/jb-$(JB_VERSION)

## Mixin configuration
MIXIN_DIR         ?= monitoring
MIXIN_RENDER_FILE ?= render.jsonnet

# Guard: define go-install-tool only if the parent Makefile has not already done so.
ifndef go-install-tool
define go-install-tool
@[ -f $(1) ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv "$$(echo "$(1)" | sed "s/-$(3)$$//")" $(1) ;\
}
endef
endif

.PHONY: mixin
mixin: mixin-vendor mixin-lint ## Install mixin deps and validate the mixin.

.PHONY: mixin-vendor
mixin-vendor: $(JB) ## Install jsonnet dependencies declared in $(MIXIN_DIR)/jsonnetfile.json.
	cd $(MIXIN_DIR) && $(abspath $(JB)) install

.PHONY: mixin-generate
mixin-generate: $(JSONNET) mixin-vendor ## Render the mixin to stdout (useful for spot-checking).
	cd $(MIXIN_DIR) && $(abspath $(JSONNET)) -J vendor -J . $(MIXIN_RENDER_FILE)

.PHONY: mixin-fmt
mixin-fmt: $(JSONNETFMT) ## Format all jsonnet/libsonnet files in $(MIXIN_DIR) in-place.
	@find $(MIXIN_DIR) -not -path '*/vendor/*' \
	  \( -name '*.jsonnet' -o -name '*.libsonnet' \) \
	  | xargs -I{} $(abspath $(JSONNETFMT)) -i {}

.PHONY: mixin-lint
mixin-lint: $(JSONNETFMT) ## Check jsonnet/libsonnet formatting in $(MIXIN_DIR) (fails if any file needs reformatting).
	@failed=0; \
	for f in $$(find $(MIXIN_DIR) -not -path '*/vendor/*' \
	    \( -name '*.jsonnet' -o -name '*.libsonnet' \)); do \
	  diff <($(abspath $(JSONNETFMT)) $$f) $$f > /dev/null \
	    || { echo "needs formatting: $$f"; failed=1; }; \
	done; \
	[ $$failed -eq 0 ] && echo "mixin-lint: OK" || exit 1

.PHONY: jsonnet
jsonnet: $(JSONNET) ## Download jsonnet locally if necessary.
$(JSONNET):
	mkdir -p $(LOCALBIN)
	$(call go-install-tool,$(JSONNET),github.com/google/go-jsonnet/cmd/jsonnet,$(JSONNET_VERSION))

.PHONY: jsonnetfmt
jsonnetfmt: $(JSONNETFMT) ## Download jsonnetfmt locally if necessary.
$(JSONNETFMT):
	mkdir -p $(LOCALBIN)
	$(call go-install-tool,$(JSONNETFMT),github.com/google/go-jsonnet/cmd/jsonnetfmt,$(JSONNET_VERSION))

.PHONY: jb
jb: $(JB) ## Download jsonnet-bundler (jb) locally if necessary.
$(JB):
	mkdir -p $(LOCALBIN)
	$(call go-install-tool,$(JB),github.com/jsonnet-bundler/jsonnet-bundler/cmd/jb,$(JB_VERSION))
