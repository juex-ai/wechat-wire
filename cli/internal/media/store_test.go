package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juex-ai/wechat-wire/cli/internal/bot"
)

func TestSaveWritesPrivateFileUnderMediaDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media")
	msg := bot.IncomingMessage{
		MessageID: 42,
		Type:      "file",
		Timestamp: time.Date(2026, 7, 27, 3, 4, 5, 0, time.UTC),
	}
	download := &bot.DownloadedMedia{
		Data:     []byte("report body"),
		Type:     "file",
		FileName: "../../quarterly report.pdf",
	}

	artifact, err := Save(root, msg, download)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !filepath.IsAbs(artifact.LocalPath) {
		t.Fatalf("LocalPath is not absolute: %q", artifact.LocalPath)
	}
	rel, err := filepath.Rel(root, artifact.LocalPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		t.Fatalf("LocalPath %q is outside root %q", artifact.LocalPath, root)
	}
	if filepath.Dir(artifact.LocalPath) != filepath.Join(root, "2026-07-27") {
		t.Fatalf("unexpected media directory: %q", filepath.Dir(artifact.LocalPath))
	}
	if strings.Contains(filepath.Base(artifact.LocalPath), "..") {
		t.Fatalf("unsafe local filename: %q", filepath.Base(artifact.LocalPath))
	}
	if artifact.FileName != "quarterly report.pdf" {
		t.Fatalf("FileName: got %q want %q", artifact.FileName, "quarterly report.pdf")
	}
	if artifact.MediaType != "file" || artifact.Size != int64(len(download.Data)) {
		t.Fatalf("unexpected artifact: %+v", artifact)
	}

	got, err := os.ReadFile(artifact.LocalPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(download.Data) {
		t.Fatalf("content: got %q want %q", got, download.Data)
	}
	info, err := os.Stat(artifact.LocalPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: got %o want 600", info.Mode().Perm())
	}
}

func TestSaveInfersMediaFileNames(t *testing.T) {
	tests := []struct {
		name       string
		mediaType  string
		format     string
		data       []byte
		wantSuffix string
	}{
		{name: "image", mediaType: "image", data: []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), wantSuffix: "image.png"},
		{name: "voice", mediaType: "voice", format: "silk", data: []byte("voice"), wantSuffix: "voice.silk"},
		{name: "video", mediaType: "video", data: []byte("video"), wantSuffix: "video.mp4"},
		{name: "file", mediaType: "file", data: []byte("file"), wantSuffix: "file.bin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "media")
			artifact, err := Save(root, bot.IncomingMessage{Type: tt.mediaType}, &bot.DownloadedMedia{
				Data:   tt.data,
				Type:   tt.mediaType,
				Format: tt.format,
			})
			if err != nil {
				t.Fatalf("Save: %v", err)
			}
			if artifact.FileName != tt.wantSuffix {
				t.Fatalf("FileName: got %q want %q", artifact.FileName, tt.wantSuffix)
			}
			if !strings.HasSuffix(artifact.LocalPath, "-"+tt.wantSuffix) {
				t.Fatalf("LocalPath does not preserve inferred name: %q", artifact.LocalPath)
			}
		})
	}
}
