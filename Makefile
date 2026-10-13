VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
# every tool in cmd/; each has its emulator in cmd/<tool>-mock
TOOLS   := $(notdir $(wildcard cmd/*-tui))

.PHONY: build test race install demo-rtpengine demo-gif gen-rtpengine clean

# builds the tools and their emulators into bin/
build:
	@for t in $(TOOLS); do \
		go build -ldflags "$(LDFLAGS)" -o bin/$$t ./cmd/$$t && \
		go build -o bin/$$t-mock ./cmd/$$t-mock || exit 1; \
	done

test:
	go vet ./...
	go test ./...

race:
	go test -race ./...

install:
	@for t in $(TOOLS); do go install -ldflags "$(LDFLAGS)" ./cmd/$$t || exit 1; done

demo-rtpengine: build
	./bin/rtpengine-tui-mock -fleet -confdir bin/rtpengine-mock-etc

# the README animation: scripts/demo-gif.py drives rtpengine-tui against the
# emulator, agg (github.com/asciinema/agg) renders the recording
demo-gif: build
	python3 scripts/demo-gif.py bin/demo.cast
	agg --font-family "JetBrains Mono,SF Mono,Menlo,DejaVu Sans Mono" --font-size 14 \
		--idle-time-limit 4 --last-frame-duration 3 bin/demo.cast docs/demo.gif

ifneq ($(wildcard cmd/opensips-tui),)
.PHONY: demo-opensips
demo-opensips: build
	./bin/opensips-tui -demo
endif

ifneq ($(wildcard cmd/homer-tui),)
.PHONY: demo-homer
demo-homer: build
	./bin/homer-tui-mock -listen 127.0.0.1:9080
endif

# Regenerates the per-version rtpengine option and NG flag tables from sipwise/rtpengine sources.
gen-rtpengine:
	./scripts/rtpengine/fetch-sources.sh bin/rtpengine-src
	python3 scripts/rtpengine/gen_options.py bin/rtpengine-src internal/rtpengine/options_gen.go
	python3 scripts/rtpengine/gen_ngflags.py bin/rtpengine-src internal/rtpengine/ngflags_gen.go
	gofmt -w internal/rtpengine/options_gen.go internal/rtpengine/ngflags_gen.go
	@go test ./internal/rtpengine -run TestOptionTranslations -count=1 >/dev/null || \
		{ go test ./internal/rtpengine -run TestOptionTranslations -count=1 | grep -E 'no translation|translated from|no such option'; \
		  echo "update the translated descriptions in internal/rtpengine/options_*.go"; }

clean:
	rm -rf bin
