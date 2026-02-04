.PHONY: build build-all clean test install

BINARY_NAME=claude-statusline
VERSION?=1.0.0

# Aktuelles OS/Arch
build:
	go build -ldflags="-s -w" -o $(BINARY_NAME)$(if $(findstring windows,$(shell go env GOOS)),.exe,) .

# Cross-compile für alle Plattformen
build-all: build-windows build-linux build-darwin

build-windows:
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/$(BINARY_NAME)-windows-amd64.exe .
	GOOS=windows GOARCH=arm64 go build -ldflags="-s -w" -o dist/$(BINARY_NAME)-windows-arm64.exe .

build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/$(BINARY_NAME)-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o dist/$(BINARY_NAME)-linux-arm64 .

build-darwin:
	GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o dist/$(BINARY_NAME)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o dist/$(BINARY_NAME)-darwin-arm64 .

clean:
	rm -f $(BINARY_NAME) $(BINARY_NAME).exe
	rm -rf dist/

test:
	@echo '{"context_window":{"current_usage":{"input_tokens":50000,"cache_creation_input_tokens":10000,"cache_read_input_tokens":5000},"context_window_size":200000},"model":{"display_name":"Claude Opus 4.5"},"workspace":{"current_dir":"C:\\Users\\test"}}' | ./$(BINARY_NAME)$(if $(findstring windows,$(shell go env GOOS)),.exe,)

# Installiere in ~/.claude/
install: build
ifeq ($(OS),Windows_NT)
	copy $(BINARY_NAME).exe "$(USERPROFILE)\.claude\$(BINARY_NAME).exe"
else
	cp $(BINARY_NAME) ~/.claude/$(BINARY_NAME)
	chmod +x ~/.claude/$(BINARY_NAME)
endif
