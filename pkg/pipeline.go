package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/streamhive/transcoder/internal/ffmpeg"
	"github.com/streamhive/transcoder/internal/queue"
	"github.com/streamhive/transcoder/internal/storage"
)

type UploadEvent struct {
	UploadID      string   `json:"uploadId"`
	UserID        string   `json:"userId"`
	Username      string   `json:"username"`
	OriginalName  string   `json:"originalFilename"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Tags          []string `json:"tags"`
	IsPrivate     bool     `json:"isPrivate"`
	Category      string   `json:"category"`
	RawVideoPath  string   `json:"rawVideoPath"`
	ContainerName string   `json:"containerName"`
	BlobURL       string   `json:"blobUrl"`
	Resolutions   []string `json:"resolutions"`
}

type Transcoder struct {
	log *zap.SugaredLogger
	az  *storage.AzureClient
	pub *queue.Publisher
}

func NewTranscoder(log *zap.SugaredLogger, az *storage.AzureClient, pub *queue.Publisher) *Transcoder {
	return &Transcoder{log: log, az: az, pub: pub}
}

func (t *Transcoder) Handle(ctx context.Context, body []byte) error {
	var evt UploadEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return fmt.Errorf("json: %w", err)
	}
	if evt.UploadID == "" || evt.UserID == "" || evt.RawVideoPath == "" {
		return fmt.Errorf("missing required fields")
	}

	work := filepath.Join(os.TempDir(), fmt.Sprintf("transcoder-%s", evt.UploadID))
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(work)

	inputPath := filepath.Join(work, "input.mp4")
	if err := t.az.DownloadTo(ctx, evt.RawVideoPath, inputPath); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	// Generate variants
	outRoot := filepath.Join(work, "hls")
	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		return err
	}

	ladder := evt.Resolutions
	if len(ladder) == 0 {
		ladder = []string{"1080p", "720p", "480p", "360p"}
	}

	for _, res := range ladder {
		resDir := filepath.Join(outRoot, res)
		if err := os.MkdirAll(resDir, 0o755); err != nil {
			return err
		}

		cmd := ffmpeg.BuildHLSCommand(ctx, inputPath, resDir, res)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		start := time.Now()
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ffmpeg %s: %w", res, err)
		}
		t.log.Infow("rendition done", "res", res, "ms", time.Since(start).Milliseconds())
	}

	// Write master playlist to outRoot
	masterPath := filepath.Join(outRoot, "master.m3u8")
	if err := os.WriteFile(masterPath, []byte(buildMaster(evt.UserID, evt.UploadID, ladder)), 0o644); err != nil {
		return err
	}

	// Upload entire HLS folder (playlists + segments)
	base := fmt.Sprintf("hls/%s/%s", evt.UserID, evt.UploadID)
	if err := t.az.UploadDir(ctx, outRoot, base); err != nil {
		return fmt.Errorf("upload hls: %w", err)
	}

	// Thumbnail
	thumbPath := filepath.Join(work, "thumb.jpg")
	thumbCmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-ss", "1", "-i", inputPath, "-frames:v", "1", thumbPath)
	var thumbnailURL string
	if err := thumbCmd.Run(); err == nil {
		thumbBlobPath := fmt.Sprintf("thumbnails/%s/%s.jpg", evt.UserID, evt.UploadID)
		if err := t.az.UploadFile(ctx, thumbPath, thumbBlobPath, "image/jpeg"); err == nil {
			thumbnailURL = fmt.Sprintf("%s/%s", os.Getenv("AZURE_PUBLIC_BASE"), thumbBlobPath)
		}
	}

	// Publish transcoded with rich metadata so catalog can fill missing fields
	out := map[string]any{
		"uploadId":         evt.UploadID,
		"userId":           evt.UserID,
		"title":            evt.Title,
		"description":      evt.Description,
		"tags":             evt.Tags,
		"category":         evt.Category,
		"isPrivate":        evt.IsPrivate,
		"originalFilename": evt.OriginalName,
		"rawVideoPath":     evt.RawVideoPath,
		"hls": map[string]any{
			"masterUrl": fmt.Sprintf("%s/%s/%s", os.Getenv("AZURE_PUBLIC_BASE"), base, "master.m3u8"),
		},
		"thumbnailUrl": thumbnailURL,
		"ready":        true,
	}
	return t.pub.PublishJSON(ctx, out)
}

func buildMaster(userId, uploadId string, ladder []string) string {
	// Bandwidth should match actual encoded bitrates (video + audio)
	bw := map[string]int{
		"1080p": 5192000, // 5000k video + 192k audio
		"720p":  2928000, // 2800k video + 128k audio
		"480p":  1496000, // 1400k video + 96k audio
		"360p":  864000,  // 800k video + 64k audio
	}
	resMap := map[string]string{"1080p": "1920x1080", "720p": "1280x720", "480p": "854x480", "360p": "640x360"}
	if len(ladder) == 0 {
		ladder = []string{"1080p", "720p", "480p", "360p"}
	}
	s := "#EXTM3U\n"
	for _, r := range ladder {
		s += fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%s\n", bw[r], resMap[r])
		s += fmt.Sprintf("%s/index.m3u8\n", r)
	}
	return s
}
