# docs.mk — reusable make targets for MkDocs-based GitHub Pages sites.
#
# Include this file from any project Makefile:
#
#   include build/docs.mk
#
# Override these variables before the include line if the defaults don't fit:
#
#   DOCS_DIR                    ?= docs
#   MKDOCS_MATERIAL_VERSION     ?= 9.6.1

##@ Docs

MKDOCS_MATERIAL_VERSION ?= 9.6.1

## Local bin directory — reuse $(LOCALBIN) if already defined by the parent Makefile.
LOCALBIN ?= $(shell pwd)/bin

DOCS_VENV ?= $(LOCALBIN)/docs-venv
MKDOCS    ?= $(DOCS_VENV)/bin/mkdocs
DOCS_DIR  ?= docs

.PHONY: docs
docs: docs-build ## Build the documentation site.

.PHONY: docs-serve
docs-serve: $(MKDOCS) ## Serve docs locally with live reload (http://127.0.0.1:8000).
	$(MKDOCS) serve

.PHONY: docs-build
docs-build: $(MKDOCS) ## Build the static documentation site into site/.
	$(MKDOCS) build --strict

.PHONY: docs-deploy
docs-deploy: $(MKDOCS) ## Deploy docs to the gh-pages branch.
	$(MKDOCS) gh-deploy --force

$(MKDOCS):
	mkdir -p $(LOCALBIN)
	python3 -m venv $(DOCS_VENV)
	$(DOCS_VENV)/bin/pip install --quiet mkdocs-material==$(MKDOCS_MATERIAL_VERSION)
