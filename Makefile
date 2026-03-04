.PHONY: build test lint proto docker-up docker-down clean

build:
	cd gateway && go build -o ../bin/gateway ./cmd/server
	cd retrieval && go build -o ../bin/retrieval ./cmd/server

test:
	cd gateway && go test ./...
	cd retrieval && go test ./...
	cd pageindex-worker && python -m pytest tests/
	cd adapter-service && python -m pytest tests/

lint:
	cd gateway && go vet ./...
	cd retrieval && go vet ./...

proto:
	bash scripts/gen-proto.sh

docker-up:
	docker compose up -d

docker-down:
	docker compose down

clean:
	rm -rf bin/
