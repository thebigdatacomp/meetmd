# MeetMD bridge — build/test helpers.

BINARY := bin/meetmd
PKG    := ./cmd/meetmd

.PHONY: build run test vet check clean

build:
	go build -o $(BINARY) $(PKG)

# run builds and launches the STABLE binary (bin/meetmd) rather than `go run`,
# whose ephemeral, changing path breaks macOS TCC permissions (Automation,
# Screen Recording) — granted once to bin/meetmd, they persist across runs.
run: build
	./$(BINARY) serve

test:
	go test ./...

vet:
	go vet ./...

# check mirrors the project's pre-commit gate.
check: vet test build

clean:
	rm -rf bin
