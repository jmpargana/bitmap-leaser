.PHONY: test build run clean

test:
	go test ./...

build:
	go build -o bitmap-leaser ./cmd/srv/

run: build
	./bitmap-leaser

clean:
	rm -f bitmap-leaser
