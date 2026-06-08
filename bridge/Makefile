# MeetMD bridge — build/test helpers.
#
# NOTE: Go 1.21 + recent macOS produce binaries without LC_UUID that dyld
# rejects ("missing LC_UUID load command"). Forcing the external system linker
# stamps the UUID. The flag is harmless elsewhere and a no-op on Go >= 1.22.

BINARY := bin/meetmd
PKG    := ./cmd/meetmd
LDFLAGS := -linkmode=external

.PHONY: build run test vet check clean

build:
	go build -ldflags=$(LDFLAGS) -o $(BINARY) $(PKG)

run:
	go run -ldflags=$(LDFLAGS) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

# check mirrors the project's pre-commit gate.
check: vet test build

clean:
	rm -rf bin
