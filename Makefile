VERSION ?= dev
LDFLAGS := -s -w -H=windowsgui -X main.version=$(VERSION)

build:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/MXHelper.exe .

vet:
	GOOS=windows GOARCH=amd64 go vet ./...

.PHONY: build vet
