BINARY=transcoder

.PHONY: deps build run docker

deps:
	go mod tidy

build:
	CGO_ENABLED=0 go build -o bin/$(BINARY) ./cmd/transcoder

run:
	AMQP_URL?=amqp://guest:guest@localhost:5672/
	AZURE_STORAGE_ACCOUNT?=
	AZURE_STORAGE_KEY?=
	AZURE_BLOB_CONTAINER?=uploadservicecontainer
	CONCURRENCY?=1
	LOG_LEVEL?=info
	FFREPORT=file=ffmpeg.log:level=32 \
		AMQP_URL=$(AMQP_URL) AZURE_STORAGE_ACCOUNT=$(AZURE_STORAGE_ACCOUNT) AZURE_STORAGE_KEY=$(AZURE_STORAGE_KEY) AZURE_BLOB_CONTAINER=$(AZURE_BLOB_CONTAINER) CONCURRENCY=$(CONCURRENCY) LOG_LEVEL=$(LOG_LEVEL) \
		go run ./cmd/transcoder

docker:
	docker build -t streamhive/transcoder:dev .
