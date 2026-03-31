.PHONY: build install test clean lint

BIN := cc-pane
INSTALL_DIR := $(or $(GOBIN),$(shell go env GOBIN),$(shell go env GOPATH)/bin)

build:
	go build -o $(BIN) .

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BIN) $(INSTALL_DIR)/$(BIN)
	@echo "Installed to $(INSTALL_DIR)/$(BIN)"

test:
	go test -v -count=1 ./...

clean:
	rm -f $(BIN)

lint:
	go vet ./...
