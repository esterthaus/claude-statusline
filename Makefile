.PHONY: build build-all clean test install install-copilot

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
	@echo '{"context_window":{"current_usage":{"input_tokens":50000,"cache_creation_input_tokens":10000,"cache_read_input_tokens":5000},"total_input_tokens":65000,"total_output_tokens":8000,"context_window_size":200000,"used_percentage":32,"remaining_percentage":68},"model":{"id":"claude-opus-4-6","display_name":"Claude Opus 4.5"},"workspace":{"current_dir":"C:/Users/test","project_dir":"C:/Users/test/project"},"rate_limits":{"five_hour":{"used_percentage":42,"resets_at":1774018800},"seven_day":{"used_percentage":18,"resets_at":1774249200}},"cost":{"total_cost_usd":0.1234,"total_duration_ms":45000,"total_api_duration_ms":2300,"total_lines_added":156,"total_lines_removed":23},"version":"1.0.80"}' | ./$(BINARY_NAME)$(if $(findstring windows,$(shell go env GOOS)),.exe,)

# Installiere in ~/.claude/
install: build
ifeq ($(OS),Windows_NT)
	copy $(BINARY_NAME).exe "$(USERPROFILE)\.claude\$(BINARY_NAME).exe"
else
	cp $(BINARY_NAME) ~/.claude/$(BINARY_NAME)
	chmod +x ~/.claude/$(BINARY_NAME)
endif

# Installiere in ~/.copilot/ (GitHub Copilot CLI)
install-copilot: build
ifeq ($(OS),Windows_NT)
	copy $(BINARY_NAME).exe "$(USERPROFILE)\.copilot\statusline.exe"
else
	cp $(BINARY_NAME) ~/.copilot/statusline
	chmod +x ~/.copilot/statusline
endif
