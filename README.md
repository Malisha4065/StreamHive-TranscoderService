# TranscoderService (StreamHive)

A Go worker that consumes upload events from RabbitMQ, downloads raw videos from Azure Blob Storage, transcodes them to HLS renditions (1080p/720p/480p/360p) using FFmpeg, uploads outputs back to Blob, and publishes a "video.transcoded" event.

## Features
- RabbitMQ consumer with prefetch and retry/DLQ strategy
- Azure Blob I/O (download raw, upload HLS + thumbnail)
- FFmpeg-based HLS ladder generation
- Master playlist generation
- Structured logging and basic Prometheus metrics on :9090/metrics

## Env
- AMQP_URL
- AMQP_EXCHANGE (default: streamhive)
- AMQP_UPLOAD_ROUTING_KEY (default: video.uploaded)
- AMQP_TRANSCODED_ROUTING_KEY (default: video.transcoded)
- AMQP_QUEUE (default: transcoder.video.uploaded)
- AZURE_STORAGE_ACCOUNT
- AZURE_STORAGE_KEY or AZURE_STORAGE_SAS_URL
- AZURE_BLOB_CONTAINER (e.g., uploadservicecontainer)
- AZURE_PUBLIC_BASE (e.g., https://account.blob.core.windows.net/container)
- TMPDIR (optional) working dir
- CONCURRENCY (default: 1)
- LOG_LEVEL (info|debug)

## Run locally
1. Install FFmpeg.
2. `make deps && make run`

## Docker
- `docker build -t streamhive/transcoder:dev .`

## K8s
- See k8s/deployment.yaml and k8s/configmap.yaml
