.PHONY: test cover cover-html cover-check build

COVERAGE_THRESHOLD := 95
COVER_PROFILE := /tmp/moonbridge-coverage.out

build:
	CGO_ENABLED=0 go build ./...

test:
	CGO_ENABLED=0 go test ./...

cover:
	CGO_ENABLED=0 go test -cover ./...

cover-check:
	@echo "Checking per-package coverage (threshold: $(COVERAGE_THRESHOLD)%)..."
	@fail=0; \
	for pkg in $$(CGO_ENABLED=0 go test -cover ./... 2>&1 | grep 'coverage:' | grep -v '0.0%' | grep -v 'no statements'); do \
		echo "$$pkg"; \
	done; \
	echo ""; \
	echo "--- Enforced packages ---"; \
	for pkg in internal/extension/plugin; do \
		pct=$$(CGO_ENABLED=0 go test -cover ./$$pkg/ 2>&1 | grep -oP '[0-9]+\.[0-9]+(?=%)'); \
		echo "$$pkg: $${pct}%"; \
		if [ $$(echo "$${pct} < $(COVERAGE_THRESHOLD)" | bc -l) -eq 1 ]; then \
			echo "  FAIL: $${pct}% < $(COVERAGE_THRESHOLD)%"; \
			fail=1; \
		fi; \
	done; \
	if [ $$fail -eq 1 ]; then echo "Coverage check FAILED"; exit 1; fi; \
	echo "Coverage check PASSED"
