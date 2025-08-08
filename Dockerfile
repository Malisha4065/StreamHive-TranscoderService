# Build stage
FROM golang:1.22 as builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/transcoder ./cmd/transcoder

# Final image with FFmpeg runtime
FROM jrottenberg/ffmpeg:6.0-ubuntu
COPY --from=builder /out/transcoder /usr/local/bin/transcoder
ENTRYPOINT ["/usr/local/bin/transcoder"]
