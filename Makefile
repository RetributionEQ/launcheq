NAME ?= launcheq
VERSION ?= 0.0.12
FILELIST_URL ?= https://raw.githubusercontent.com/retributioneq/launcheq/rof
PATCHER_URL ?= https://github.com/retributioneq/launcheq/releases/latest/download/
EXE_NAME ?= launcheq.exe
SHELL := /bin/bash

# CICD triggers this
.PHONY: set-variable
set-version:
	@echo "VERSION=${VERSION}" >> $$GITHUB_ENV

#go install golang.org/x/tools/cmd/goimports@latest
#go install github.com/fzipp/gocyclo/cmd/gocyclo@latest
#go install golang.org/x/lint/golint@latest
#go install honnef.co/go/tools/cmd/staticcheck@v0.2.2

sanitize:
	@echo "sanitize: checking for errors"
	rm -rf vendor/
	go vet -tags ci ./...
	test -z $(goimports -e -d . | tee /dev/stderr)
	gocyclo -over 30 .
	golint -set_exit_status $(go list -tags ci ./...)
	staticcheck -go 1.14 ./...
	go test -tags ci -covermode=atomic -coverprofile=coverage.out ./...
    coverage=`go tool cover -func coverage.out | grep total | tr -s '\t' | cut -f 3 | grep -o '[^%]*'`

run: sanitize
	@echo "run: building"
	mkdir -p bin
	cd bin && go run ../main.go

#go install github.com/tc-hib/go-winres@latest
bundle:
	go-winres simply --icon launcheq.png

.PHONY: build-all
build-all: sanitize build-prepare build-linux build-darwin build-windows	
.PHONY: build-prepare
build-prepare:
	@echo "Preparing talkeq ${VERSION}"
	@rm -rf bin/*
	@-mkdir -p bin/
.PHONY: build-darwin
build-darwin:
	@echo "Building darwin ${VERSION}"
	@GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -buildmode=pie -ldflags="-X main.Version=${VERSION} -s -w" -o bin/${NAME}-darwin-x64 main.go
.PHONY: build-linux
build-linux:
	@echo "Building Linux ${VERSION}"
	@GOOS=linux GOARCH=amd64 go build -buildmode=pie -ldflags="-X main.Version=${VERSION} -w" -o bin/${NAME}-linux-x64 main.go		
.PHONY: build-windows
build-windows:
	@echo "Building Windows ${VERSION}"
	go install github.com/akavel/rsrc@latest
	rsrc -ico launcheq.ico -manifest launcheq.exe.manifest
	GOOS=windows GOARCH=amd64 go build -buildmode=pie -ldflags="-X main.Version=${VERSION} -X main.PatcherURL=${PATCHER_URL} -s -w" -o bin/${NAME}.exe

build-share:
	make build-windows
	cp bin/${NAME}.exe /Volumes/share/launcheq/
maps:
	@-cd rof && zip -r maps.zip maps
	@-mv rof/maps.zip bin/

build-windows-if-needed:
	wget --no-verbose -O runifnew https://github.com/xackery/runifnew/releases/latest/download/runifnew-linux
	mkdir -p bin
	chmod +x runifnew
	./runifnew -cmd "make build-windows" -url "${PATCHER_URL}/${EXE_NAME}" -urlPath "bin/launcheq.exe" main.go client/ config/