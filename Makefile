IMG ?= gke-vpc-fwr-rules-controller:latest

.PHONY: build test test-verbose test-cover vet docker-build docker-push deploy

build:
	go build -o bin/controller .

test:
	go test ./... -race -count=1

test-verbose:
	go test ./... -race -count=1 -v

test-cover:
	go test ./... -race -count=1 -coverprofile=coverage.out
	go tool cover -func=coverage.out

vet:
	go vet ./...

docker-build:
	docker build -t $(IMG) .

docker-push:
	docker push $(IMG)

deploy:
	kubectl apply -f deploy/
