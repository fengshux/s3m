BINARY   := s3m
GOOS     := linux
BUILDDIR := build

.PHONY: all amd64 arm64 clean

all: amd64 arm64

amd64:
	GOOS=$(GOOS) GOARCH=amd64 go build -o $(BUILDDIR)/$(BINARY)-linux-amd64 .

arm64:
	GOOS=$(GOOS) GOARCH=arm64 go build -o $(BUILDDIR)/$(BINARY)-linux-arm64 .

clean:
	rm -rf $(BUILDDIR)
