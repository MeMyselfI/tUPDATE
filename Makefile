VERSION ?= 0.1.0-dev
GO      ?= go
UPX     ?= upx
LDFLAGS  = -s -w -X main.version=$(VERSION)
GOFLAGS  = -trimpath -ldflags "$(LDFLAGS)"
PKG      = ./cmd/updater
DIST     = dist

SYSO_DIR = cmd/updater
SYSO_FILES = \
  $(SYSO_DIR)/resource_windows_386.syso \
  $(SYSO_DIR)/resource_windows_amd64.syso \
  $(SYSO_DIR)/resource_windows_arm.syso \
  $(SYSO_DIR)/resource_windows_arm64.syso

# Targets that go into UPX-packing. Excluded:
#   - darwin: UPX breaks codesigning/notarization.
#   - windows-arm64: UPX 5.x rejects win64/arm64 ("not yet supported").
UPX_TARGETS = \
  $(DIST)/updater-windows-amd64.exe \
  $(DIST)/updater-linux-amd64 \
  $(DIST)/updater-linux-arm64

ALL_TARGETS = \
  $(UPX_TARGETS) \
  $(DIST)/updater-windows-arm64.exe \
  $(DIST)/updater-darwin-amd64 \
  $(DIST)/updater-darwin-arm64 \
  $(DIST)/updater-darwin-universal

.PHONY: all build-all test vet fmt clean upx-all windows-manifest help

all: build-all

help:
	@echo "make build-all        Cross-compile all 7 binaries (incl. darwin-universal)"
	@echo "make test             Run go test ./..."
	@echo "make vet              Run go vet ./..."
	@echo "make fmt              Run gofmt -l (lists files needing fmt)"
	@echo "make windows-manifest Regenerate cmd/updater/resource_windows_*.syso"
	@echo "make upx-all          UPX-pack windows + linux binaries (skips darwin)"
	@echo "make clean            Remove dist/"

build-all: $(ALL_TARGETS)
	@ls -lh $(ALL_TARGETS)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	@gofmt -l . | tee /dev/stderr | (! grep -q .)

clean:
	rm -rf $(DIST)

windows-manifest: $(SYSO_FILES)

$(SYSO_FILES): $(SYSO_DIR)/versioninfo.json $(SYSO_DIR)/manifest.xml
	cd $(SYSO_DIR) && $(GO) run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest \
	    -64=true -platform-specific=true versioninfo.json

$(DIST):
	mkdir -p $(DIST)

# Windows builds depend on .syso files (auto-linked when GOOS=windows).
$(DIST)/updater-windows-amd64.exe: $(DIST) $(SYSO_FILES)
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -o $@ $(PKG)

$(DIST)/updater-windows-arm64.exe: $(DIST) $(SYSO_FILES)
	GOOS=windows GOARCH=arm64 $(GO) build $(GOFLAGS) -o $@ $(PKG)

$(DIST)/updater-darwin-amd64: $(DIST)
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -o $@ $(PKG)

$(DIST)/updater-darwin-arm64: $(DIST)
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -o $@ $(PKG)

$(DIST)/updater-linux-amd64: $(DIST)
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -o $@ $(PKG)

$(DIST)/updater-linux-arm64: $(DIST)
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -o $@ $(PKG)

# Universal Mac binary: fat Mach-O containing both amd64 and arm64.
# Requires lipo (preinstalled on macOS) on the build host.
$(DIST)/updater-darwin-universal: $(DIST)/updater-darwin-amd64 $(DIST)/updater-darwin-arm64
	lipo -create -output $@ $^

# UPX requires the upx binary in PATH. brew install upx (or apt install upx-ucl).
# Darwin binaries are NOT packed: UPX breaks codesigning/notarization on macOS.
upx-all: $(UPX_TARGETS)
	@for f in $(UPX_TARGETS); do \
	    echo "==> UPX: $$f"; \
	    $(UPX) --best --lzma "$$f" || exit 1; \
	done
	@ls -lh $(UPX_TARGETS)
