VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
VERSION_PKG := gok-pi/internal/remote/wsclient
LDFLAGS := -X $(VERSION_PKG).version=$(VERSION)

.PHONY: build run controlserver agentupdater build-amd64 build-arm64 build-all

build:
	go build -ldflags "$(LDFLAGS)" -o gok ./cmd/gok

build-amd64:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o .build/amd64/gok ./cmd/gok
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o .build/amd64/agentupdater ./cmd/agentupdater

build-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o .build/arm64/gok ./cmd/gok
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o .build/arm64/agentupdater ./cmd/agentupdater

build-all: build-amd64 build-arm64

run:
	go run -ldflags "$(LDFLAGS)" ./cmd/gok $(ARGS)

controlserver:
	go build -o controlserver ./cmd/controlserver

agentupdater:
	go build -o agentupdater ./cmd/agentupdater
