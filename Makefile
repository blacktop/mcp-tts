.PHONY: bump
bump:
	@echo "🚀 Bumping Version"
	$(eval NEXT := $(shell svu patch))
	git tag $(NEXT) -m "$(NEXT)"
	git push --tags

.PHONY: build
build:
	@echo "🚀 Building Version $(shell svu current)"
	go build -o mcp-tts main.go

.PHONY: release
release:
	@echo "🚀 Releasing Version $(shell svu current)"
	goreleaser build --id default --clean --snapshot --single-target --output dist/mcp-tts

.PHONY: snapshot
snapshot:
	@echo "🚀 Snapshot Version $(shell svu current)"
	goreleaser --clean --timeout 60m --snapshot

.PHONY: test
test:
	@echo "🧪 Running Tests..."
	go test -v ./...
