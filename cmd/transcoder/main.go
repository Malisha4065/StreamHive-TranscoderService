package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/streamhive/transcoder/internal/queue"
	"github.com/streamhive/transcoder/internal/storage"
	"github.com/streamhive/transcoder/pkg"
)

func main() {
	var metricsAddr string
	flag.StringVar(&metricsAddr, "metrics", ":9090", "metrics listen address")
	flag.Parse()

	logger, _ := zap.NewProduction()
	defer logger.Sync()
	log := logger.Sugar()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Metrics server
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{Addr: metricsAddr, Handler: mux}
	go func() {
		log.Infof("metrics listening on %s", metricsAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("metrics server: %v", err)
		}
	}()

	consumer, err := queue.NewConsumerFromEnv(log)
	if err != nil {
		log.Fatalf("queue init: %v", err)
	}
	defer consumer.Close()

	// publisher for transcoded events
	pub, err := queue.NewPublisher(consumer.Conn(), consumer.Exchange(), getenv("AMQP_TRANSCODED_ROUTING_KEY", "video.transcoded"))
	if err != nil {
		log.Fatalf("publisher init: %v", err)
	}
	defer pub.Close()

	az, err := storage.NewAzureClientFromEnv()
	if err != nil {
		log.Fatalf("azure: %v", err)
	}

	pipeline := pkg.NewTranscoder(log, az, pub)

	concurrency := queue.GetEnvInt("CONCURRENCY", 1)
	log.Infof("starting consumer with concurrency=%d", concurrency)

	err = consumer.Consume(ctx, concurrency, func(b []byte) error {
		var check map[string]any
		if err := json.Unmarshal(b, &check); err != nil {
			return fmt.Errorf("invalid json: %w", err)
		}
		log.Infow("upload event", "uploadId", check["uploadId"], "userId", check["userId"])
		return pipeline.Handle(ctx, b)
	})
	if err != nil {
		log.Fatalf("consume error: %v", err)
	}

	// graceful shutdown metrics server
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctxTimeout)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
