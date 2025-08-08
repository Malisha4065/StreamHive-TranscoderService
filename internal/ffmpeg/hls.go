package ffmpeg

import (
	"context"
	"fmt"
	"os/exec"
)

// Variant preset for HLS ladder
var Presets = map[string][]string{
	"1080p": {"-vf", "scale=-2:1080", "-b:v", "5000k", "-maxrate", "5350k", "-bufsize", "7500k", "-b:a", "192k"},
	"720p":  {"-vf", "scale=-2:720", "-b:v", "2800k", "-maxrate", "2996k", "-bufsize", "4200k", "-b:a", "128k"},
	"480p":  {"-vf", "scale=-2:480", "-b:v", "1400k", "-maxrate", "1498k", "-bufsize", "2100k", "-b:a", "96k"},
	"360p":  {"-vf", "scale=-2:360", "-b:v", "800k", "-maxrate", "856k", "-bufsize", "1200k", "-b:a", "64k"},
}

func BuildHLSCommand(ctx context.Context, input, outDir string, res string) *exec.Cmd {
	args := []string{
		"-y",
		"-i", input,
		"-preset", "veryfast",
		"-g", "48", "-keyint_min", "48", "-sc_threshold", "0",
		"-c:a", "aac", "-ar", "48000",
	}
	args = append(args, Presets[res]...)
	args = append(args, []string{
		"-hls_time", "6",
		"-hls_playlist_type", "vod",
		"-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments",
		"-f", "hls",
		fmt.Sprintf("%s/index.m3u8", outDir),
	}...)
	return exec.CommandContext(ctx, "ffmpeg", args...)
}

func GenerateThumbnail(ctx context.Context, input, outPath string) *exec.Cmd {
	// grab a frame at 3s
	return exec.CommandContext(ctx, "ffmpeg", "-y", "-ss", "3", "-i", input, "-frames:v", "1", "-q:v", "2", outPath)
}
