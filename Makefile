VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILT   ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

LDFLAGS := -ldflags "\
  -X github.com/seungpyoson/waggle/cmd.Version=$(VERSION) \
  -X github.com/seungpyoson/waggle/cmd.Commit=$(COMMIT) \
  -X github.com/seungpyoson/waggle/cmd.BuildTime=$(BUILT)"

.PHONY: build test clean

build:
	go build $(LDFLAGS) -o waggle .

test:
	go test ./... -v

clean:
	rm -f waggle
