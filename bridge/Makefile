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

# run builds and launches the STABLE binary (bin/meetmd) rather than `go run`,
# whose ephemeral, changing path breaks macOS TCC permissions (Automation,
# Screen Recording) — granted once to bin/meetmd, they persist across runs.
run: build
	./$(BINARY) serve

test:
	go test -ldflags=$(LDFLAGS) ./...

vet:
	go vet ./...

# check mirrors the project's pre-commit gate.
check: vet test build

clean:
	rm -rf bin
