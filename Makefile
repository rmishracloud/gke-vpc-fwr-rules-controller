IMG ?= gke-vpc-fwr-rules-controller:latest

.PHONY: build test vet docker-build docker-push deploy

build:
	go build -o bin/controller .

test:
	go test ./... -v

vet:
	go vet ./...

docker-build:
	docker build -t $(IMG) .

docker-push:
	docker push $(IMG)

deploy:
	kubectl apply -f deploy/
