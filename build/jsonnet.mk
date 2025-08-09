
OUTPUT_DIR ?= ${PWD}/gen
ABS_OUTPUT_DIR := $(shell realpath $(OUTPUT_DIR))

IMPORTS=$(shell find libs -name config.jsonnet | xargs -I {} echo "(import '{}'),")

PAGES ?= false
GEN_COMMIT ?= false
DIFF ?= true
GITHUB_SHA ?= $(shell git rev-parse HEAD)
GIT_AUTHOR_NAME ?= $(shell git --no-pager log --format=format:'%an' -n 1)
GIT_AUTHOR_EMAIL ?= $(shell git --no-pager log --format=format:'%ae' -n 1)
GIT_COMMITTER_NAME ?= $(shell git --no-pager log --format=format:'%an' -n 1)
GIT_COMMITTER_EMAIL ?= $(shell git --no-pager log --format=format:'%ae' -n 1)

.DEFAULT_GOAL: default
default:

.PHONY: jsonnet
jsonnet: ## Generate k8s jsonnet libs
	@echo "Generating k8s jsonnet libs..."
	@echo TODO: docker build -t ghcr.io/jsonnet-libs/k8s-gen:0.0.8 -f Dockerfile .
	@echo "Running k8s jsonnet libs generator..."
	@docker run --rm --user 2050:2000 -v ./libs/nodemanager:/config -v ./jsonnet/gen:/output -v ./:/src -e DIFF=false -e GEN_COMMIT=false -e SSH_KEY= ghcr.io/jsonnet-libs/k8s-gen:0.0.8 /config /output

.PHONY: .github/workflows/main.yml
.github/workflows/main.yml:
	jsonnet -c -m . -S -J . --tla-code "libs=[$(IMPORTS)]" jsonnet/github_action.jsonnet

## Requires go-jsonnet for -c flag
.PHONY: tf/main.tf.json
tf/main.tf.json:
	jsonnet -c -m . -S -J . --tla-code "pages=$(PAGES)" --tla-code "libs=[$(IMPORTS)]" jsonnet/terraform.jsonnet

all: build libs/*

libs/*:
	mkdir -p $(ABS_OUTPUT_DIR) && \
	./bin/docker.sh \
		-v $(shell realpath $@):/config \
		-v $(ABS_OUTPUT_DIR):/output \
		-e DIFF="$(DIFF)" \
		-e GEN_COMMIT="$(GEN_COMMIT)" \
		-e GITHUB_SHA="$(GITHUB_SHA)" \
		-e GIT_AUTHOR_NAME="$(GIT_AUTHOR_NAME)" \
		-e GIT_AUTHOR_EMAIL="$(GIT_AUTHOR_EMAIL)" \
		-e GIT_COMMITTER_NAME="$(GIT_COMMITTER_NAME)" \
		-e GIT_COMMITTER_EMAIL="$(GIT_COMMITTER_EMAIL)" \
		-e SSH_KEY="$${SSH_KEY}" \
		$(IMAGE_PREFIX)/$(IMAGE_NAME):$(IMAGE_TAG) /config /output

clean:
	rm -f .github/workflows/main.yml:
	rm -f tf/main.tf.json

configure: clean .github/workflows/main.yml tf/main.tf.json
