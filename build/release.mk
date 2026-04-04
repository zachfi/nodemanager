RELEASE_SCRIPT ?= ./scripts/release.sh

REL_CMD  ?= goreleaser
DIST_DIR ?= ./dist
SRCDIR   ?= .

# Example usage: make release version=0.11.0
release: build
	@echo "=== $(PROJECT_NAME) === [ release          ]: Generating release."
	git fetch --tags
	$(REL_CMD) release

release-clean:
	@echo "=== $(PROJECT_NAME) === [ release-clean    ]: distribution files..."
	@rm -rfv $(DIST_DIR) $(SRCDIR)/tmp

release-publish: clean tools docker-login release-notes
	@echo "=== $(PROJECT_NAME) === [ release-publish  ]: Publishing release via $(REL_CMD)"
	$(REL_CMD) --release-notes=$(SRCDIR)/tmp/$(RELEASE_NOTES_FILE)

# Local Snapshot
snapshot: release-clean
	@echo "=== $(PROJECT_NAME) === [ snapshot         ]: Creating release via $(REL_CMD)"
	@echo "=== $(PROJECT_NAME) === [ snapshot         ]:   THIS WILL NOT BE PUBLISHED!"
	$(REL_CMD) --skip=publish --snapshot

release-downstream: ## Propagate the current tag to nodemanager-bin, aur, and jsonnet-libs
	@echo "=== $(PROJECT_NAME) === [ release-downstream ]: Updating downstream repos"
	./tools/release-downstream.sh

test-release-downstream: ## Test the release-downstream script against local bare repos (no network, no docker)
	@echo "=== $(PROJECT_NAME) === [ test-release-downstream ]: Running release-downstream tests"
	bash tools/test-release-downstream.sh

.PHONY: release release-clean release-publish snapshot release-downstream test-release-downstream
