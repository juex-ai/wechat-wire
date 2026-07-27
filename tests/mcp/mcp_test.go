package mcp

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPFakeMessageFlow(t *testing.T) {
	bin := binary(t)
	dataDir := t.TempDir()

	server := startMCPServer(t, bin, dataDir, map[string]string{
		"WECHAT_WIRE_FAKE":               "1",
		"WECHAT_WIRE_FAKE_MESSAGES_JSON": `[{"user_id":"user-1","text":"hello mcp","type":"text","context_token":"ctx-1"}]`,
	})

	server.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"experimental": map[string]any{"claude/channel": map[string]any{}},
			},
			"clientInfo": map[string]any{"name": "wechat-wire-e2e", "version": "test"},
		},
	})
	assertNoRPCError(t, server.waitResponse(1))

	server.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{}})
	notification := server.waitNotification("notifications/claude/channel", func(params map[string]any) bool {
		content, _ := params["content"].(string)
		return strings.Contains(content, "hello mcp") && strings.Contains(content, "user-1")
	})
	assertChannelEvent(t, notification, "message")

	callTool(t, server, 2, "wechat_wire_list_users", map[string]any{})
	listResp := server.waitResponse(2)
	assertNoRPCError(t, listResp)
	assertToolTextContains(t, listResp, "user-1")

	callTool(t, server, 3, "wechat_wire_send_message", map[string]any{"user_id": "user-1", "text": "reply"})
	sendResp := server.waitResponse(3)
	assertNoRPCError(t, sendResp)
	assertToolTextContains(t, sendResp, "ok: sent to user-1")

	attachmentPath := filepath.Join(dataDir, "report.txt")
	if err := os.WriteFile(attachmentPath, []byte("attachment from mcp"), 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	callTool(t, server, 4, "wechat_wire_send_attachment", map[string]any{
		"user_id": "user-1",
		"path":    attachmentPath,
		"caption": "report",
	})
	attachmentResp := server.waitResponse(4)
	assertNoRPCError(t, attachmentResp)
	assertToolTextContains(t, attachmentResp, "ok: sent report.txt (19 bytes) to user-1")
}

func TestMCPMediaMessageDownloadsToLocalPath(t *testing.T) {
	bin := binary(t)
	dataDir := t.TempDir()
	const imageBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

	server := startMCPServer(t, bin, dataDir, map[string]string{
		"WECHAT_WIRE_FAKE": "1",
		"WECHAT_WIRE_FAKE_MESSAGES_JSON": `[{
			"user_id":"user-media",
			"text":"[image]",
			"type":"image",
			"context_token":"ctx-media",
			"message_id":12345,
			"media_base64":"` + imageBase64 + `"
		}]`,
	})

	server.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"experimental": map[string]any{"claude/channel": map[string]any{}},
			},
			"clientInfo": map[string]any{"name": "wechat-wire-e2e", "version": "test"},
		},
	})
	assertNoRPCError(t, server.waitResponse(1))

	server.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{}})
	notification := server.waitNotification("notifications/claude/channel", func(params map[string]any) bool {
		meta, _ := params["meta"].(map[string]any)
		path, _ := meta["local_path"].(string)
		return path != ""
	})
	assertChannelEvent(t, notification, "message")

	params := notification["params"].(map[string]any)
	content, _ := params["content"].(string)
	meta := params["meta"].(map[string]any)
	localPath, _ := meta["local_path"].(string)
	if !filepath.IsAbs(localPath) {
		t.Fatalf("local_path is not absolute: %q", localPath)
	}
	mediaDir := filepath.Join(dataDir, ".config", "wechat-wire", "media")
	rel, err := filepath.Rel(mediaDir, localPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		t.Fatalf("local_path %q is outside media dir %q", localPath, mediaDir)
	}
	if !strings.Contains(content, "local_path: "+localPath) {
		t.Fatalf("notification content missing local path:\n%s", content)
	}
	if got := meta["media_type"]; got != "image" {
		t.Fatalf("media_type: got %v want image", got)
	}
	attachments, ok := params["attachments"].([]any)
	if !ok || len(attachments) != 1 {
		t.Fatalf("attachments: got %#v want one attachment", params["attachments"])
	}
	attachment, ok := attachments[0].(map[string]any)
	if !ok || attachment["path"] != localPath {
		t.Fatalf("attachment: got %#v want path %q", attachments[0], localPath)
	}

	want, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	got, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read downloaded media: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("downloaded media content mismatch: got %d bytes want %d", len(got), len(want))
	}
	info, err := os.Stat(localPath)
	if err != nil {
		t.Fatalf("stat downloaded media: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("downloaded media mode: got %o want 600", info.Mode().Perm())
	}
}

func binary(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("WECHAT_WIRE_BIN"); v != "" {
		return v
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return filepath.Join(root, "bin", "wechat-wire")
}

func callTool(t *testing.T, c *stdioMCP, id int, name string, args map[string]any) {
	t.Helper()
	c.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	})
}

type rpcMessage map[string]any

type stdioMCP struct {
	t      *testing.T
	cancel context.CancelFunc
	stdin  io.WriteCloser
	msgs   chan rpcMessage
	done   chan error
}

func startMCPServer(t *testing.T, bin, dataDir string, extraEnv map[string]string) *stdioMCP {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "mcp")
	cmd.Env = append(os.Environ(), "WECHAT_WIRE_DIR="+dataDir)
	for k, v := range extraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		t.Fatalf("stderr pipe: %v", err)
	}

	c := &stdioMCP{t: t, cancel: cancel, stdin: stdin, msgs: make(chan rpcMessage, 32), done: make(chan error, 1)}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start MCP server: %v", err)
	}
	go c.readStdout(stdout)
	go func() {
		raw, _ := io.ReadAll(stderr)
		if len(raw) > 0 {
			t.Logf("mcp stderr:\n%s", raw)
		}
	}()
	go func() {
		c.done <- cmd.Wait()
		close(c.done)
	}()
	t.Cleanup(c.stop)
	return c
}

func (c *stdioMCP) stop() {
	_ = c.stdin.Close()
	c.cancel()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		c.t.Log("MCP subprocess did not exit within timeout")
	}
}

func (c *stdioMCP) readStdout(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var msg rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			c.t.Logf("invalid stdout JSON %q: %v", scanner.Text(), err)
			continue
		}
		c.msgs <- msg
	}
	close(c.msgs)
}

func (c *stdioMCP) send(msg map[string]any) {
	c.t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		c.t.Fatalf("marshal request: %v", err)
	}
	data = append(data, '\n')
	if _, err := c.stdin.Write(data); err != nil {
		c.t.Fatalf("write request: %v", err)
	}
}

func (c *stdioMCP) waitResponse(id int) rpcMessage {
	c.t.Helper()
	return c.waitFor(20*time.Second, func(msg rpcMessage) bool {
		got, ok := msg["id"].(float64)
		return ok && int(got) == id
	})
}

func (c *stdioMCP) waitNotification(method string, pred func(map[string]any) bool) rpcMessage {
	c.t.Helper()
	return c.waitFor(20*time.Second, func(msg rpcMessage) bool {
		if msg["method"] != method {
			return false
		}
		params, ok := msg["params"].(map[string]any)
		return ok && pred(params)
	})
}

func (c *stdioMCP) waitFor(timeout time.Duration, pred func(rpcMessage) bool) rpcMessage {
	c.t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case msg, ok := <-c.msgs:
			if !ok {
				c.t.Fatal("MCP stdout closed")
			}
			if pred(msg) {
				return msg
			}
		case <-timer.C:
			c.t.Fatal("timed out waiting for MCP message")
		}
	}
}

func assertNoRPCError(t *testing.T, msg rpcMessage) {
	t.Helper()
	if errVal, ok := msg["error"]; ok {
		t.Fatalf("unexpected RPC error: %+v", errVal)
	}
}

func assertChannelEvent(t *testing.T, msg rpcMessage, eventType string) {
	t.Helper()
	params, ok := msg["params"].(map[string]any)
	if !ok {
		t.Fatalf("notification missing params: %+v", msg)
	}
	meta, ok := params["meta"].(map[string]any)
	if !ok {
		t.Fatalf("notification missing meta: %+v", msg)
	}
	if got := meta["event_type"]; got != eventType {
		t.Fatalf("event_type: got %v want %s", got, eventType)
	}
}

func assertToolTextContains(t *testing.T, msg rpcMessage, want string) {
	t.Helper()
	result, ok := msg["result"].(map[string]any)
	if !ok {
		t.Fatalf("response missing result: %+v", msg)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("response missing content: %+v", msg)
	}
	var joined strings.Builder
	for _, item := range content {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		fmt.Fprintf(&joined, "%v\n", obj["text"])
	}
	if !strings.Contains(joined.String(), want) {
		t.Fatalf("tool text missing %q:\n%s", want, joined.String())
	}
}
