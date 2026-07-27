// Package media persists downloaded inbound message media.
package media

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/juex-ai/wechat-wire/cli/internal/bot"
)

// Artifact describes one media file saved for an inbound message.
type Artifact struct {
	LocalPath string `json:"local_path"`
	MediaType string `json:"media_type"`
	FileName  string `json:"file_name"`
	Size      int64  `json:"size"`
}

// Save writes decrypted media under a date-partitioned private directory.
func Save(root string, msg bot.IncomingMessage, download *bot.DownloadedMedia) (*Artifact, error) {
	if download == nil {
		return nil, nil
	}
	if root == "" {
		return nil, fmt.Errorf("media directory is required")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving media directory: %w", err)
	}
	timestamp := msg.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	dayDir := filepath.Join(absRoot, timestamp.Local().Format("2006-01-02"))
	if err := makePrivateDir(absRoot); err != nil {
		return nil, err
	}
	if err := makePrivateDir(dayDir); err != nil {
		return nil, err
	}

	mediaType := download.Type
	if mediaType == "" {
		mediaType = msg.Type
	}
	fileName := sanitizeFilename(download.FileName)
	if fileName == "" {
		fileName = defaultFileName(mediaType, download.Format, download.Data)
	}
	stem := timestamp.UTC().Format("20060102T150405.000000000Z")
	if msg.MessageID != 0 {
		stem += "-" + strconv.FormatInt(msg.MessageID, 10)
	}

	file, err := os.CreateTemp(dayDir, stem+"-*-"+fileName)
	if err != nil {
		return nil, fmt.Errorf("creating media file: %w", err)
	}
	localPath := file.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(localPath)
		}
	}()

	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("setting media file permissions: %w", err)
	}
	if _, err := file.Write(download.Data); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("writing media file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("syncing media file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("closing media file: %w", err)
	}
	keep = true

	return &Artifact{
		LocalPath: localPath,
		MediaType: mediaType,
		FileName:  fileName,
		Size:      int64(len(download.Data)),
	}, nil
}

func makePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("creating media directory: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("setting media directory permissions: %w", err)
	}
	return nil
}

func sanitizeFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return truncateUTF8(name, 120)
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	for len(value) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return strings.TrimRight(value, " .")
}

func defaultFileName(mediaType, format string, data []byte) string {
	switch mediaType {
	case "image":
		switch http.DetectContentType(data) {
		case "image/jpeg":
			return "image.jpg"
		case "image/png":
			return "image.png"
		case "image/gif":
			return "image.gif"
		case "image/webp":
			return "image.webp"
		default:
			return "image.bin"
		}
	case "voice":
		ext := sanitizeFilename(strings.TrimPrefix(format, "."))
		if ext == "" || strings.Contains(ext, ".") {
			ext = "silk"
		}
		return "voice." + ext
	case "video":
		return "video.mp4"
	default:
		switch http.DetectContentType(data) {
		case "application/pdf":
			return "file.pdf"
		case "application/zip":
			return "file.zip"
		default:
			return "file.bin"
		}
	}
}
