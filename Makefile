.PHONY: test cover cover-html cover-check build webui-install webui-test webui-build build-with-webui desktop-install-deps desktop-sidecar desktop-sidecar-fast desktop-dev desktop-dev-fast desktop-build desktop-build-app desktop-icons desktop-app-install

COVERAGE_THRESHOLD := 95
COVER_PROFILE := /tmp/moonbridge-coverage.out

build:
	CGO_ENABLED=0 go build ./...

webui-install:
	npm --prefix webui install

webui-test:
	npm --prefix webui test

webui-build:
	npm --prefix webui run build
	rm -rf internal/service/webui/dist
	mkdir -p internal/service/webui/dist
	cp -R webui/dist/. internal/service/webui/dist/

build-with-webui: webui-build
	CGO_ENABLED=0 go build ./...

# Desktop (Tauri) — see docs/desktop.md
desktop-install-deps:
	npm --prefix webui install
	npm --prefix desktop install

desktop-sidecar:
	bash desktop/scripts/build-sidecar.sh

desktop-sidecar-fast:
	SKIP_WEBUI=1 bash desktop/scripts/build-sidecar.sh

desktop-dev: desktop-sidecar
	npm --prefix desktop run tauri -- dev

desktop-dev-fast: desktop-sidecar-fast
	npm --prefix desktop run tauri -- dev

desktop-build: desktop-sidecar
	npm --prefix desktop run tauri -- build

desktop-build-app: desktop-sidecar
	npm --prefix desktop run tauri -- build -- --bundles app

desktop-icons:
	npm --prefix desktop run icons

desktop-app-install: desktop-build-app
	@APP='desktop/src-tauri/target/debug/bundle/macos/Moon Bridge.app'; \
	if [ ! -d "$$APP" ]; then APP='desktop/src-tauri/target/release/bundle/macos/Moon Bridge.app'; fi; \
	test -d "$$APP" || { echo "missing Moon Bridge.app — run make desktop-build-app"; exit 1; }; \
	rm -rf '/Applications/Moon Bridge.app'; \
	cp -R "$$APP" /Applications/; \
	echo 'Installed /Applications/Moon Bridge.app'

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
