VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BINARY  := pgcopydb-tui
LDFLAGS := -ldflags "-X github.com/dimitri/pgcopydb/contrib/tui/cmd.Version=$(VERSION)"

.PHONY: build clean tidy run

build:
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BINARY) .

clean:
	rm -f $(BINARY)

tidy:
	go mod tidy

run: build
	./$(BINARY) $(ARGS)
