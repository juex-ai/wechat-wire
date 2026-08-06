package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/juex-ai/wechat-wire/cli/internal/bot"
)

func TestEnsureBotSerializesLoginAndReusesClient(t *testing.T) {
	t.Setenv("WECHAT_WIRE_DIR", t.TempDir())

	client := newBlockingClient()
	var factoryCalls atomic.Int32
	server := NewServer("test", true, func(opts bot.Options) bot.Client {
		factoryCalls.Add(1)
		return client
	})

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.mu.Lock()
	server.runCtx = runCtx
	server.mu.Unlock()

	const callers = 8
	start := make(chan struct{})
	results := make(chan bot.Client, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := server.ensureBot(context.Background())
			if err != nil {
				errs <- err
				return
			}
			results <- got
		}()
	}

	close(start)
	select {
	case <-client.loginStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("login did not start")
	}
	close(client.releaseLogin)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("ensureBot: %v", err)
	}
	for got := range results {
		if got != client {
			t.Fatalf("ensureBot returned %T, want shared blockingClient", got)
		}
	}
	if got := factoryCalls.Load(); got != 1 {
		t.Fatalf("factory calls: got %d want 1", got)
	}
	if got := client.loginCalls.Load(); got != 1 {
		t.Fatalf("login calls: got %d want 1", got)
	}
	waitForRunCall(t, client, 1)
}

func TestEnsureBotWaiterCanCancelDuringSharedLogin(t *testing.T) {
	t.Setenv("WECHAT_WIRE_DIR", t.TempDir())

	client := newBlockingClient()
	var factoryCalls atomic.Int32
	server := NewServer("test", true, func(opts bot.Options) bot.Client {
		factoryCalls.Add(1)
		return client
	})

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	server.mu.Lock()
	server.runCtx = runCtx
	server.mu.Unlock()

	first := make(chan error, 1)
	go func() {
		_, err := server.ensureBot(context.Background())
		first <- err
	}()

	select {
	case <-client.loginStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("login did not start")
	}

	waitCtx, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	waiter := make(chan error, 1)
	go func() {
		_, err := server.ensureBot(waitCtx)
		waiter <- err
	}()

	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter error: got %v want context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("canceled waiter did not return while login was in progress")
	}

	close(client.releaseLogin)
	if err := <-first; err != nil {
		t.Fatalf("first ensureBot: %v", err)
	}
	if got := factoryCalls.Load(); got != 1 {
		t.Fatalf("factory calls: got %d want 1", got)
	}
	if got := client.loginCalls.Load(); got != 1 {
		t.Fatalf("login calls: got %d want 1", got)
	}
	waitForRunCall(t, client, 1)
}

func TestOnInitializedDoesNotBlockOnLogin(t *testing.T) {
	t.Setenv("WECHAT_WIRE_DIR", t.TempDir())

	client := newBlockingClient()
	server := NewServer("test", true, func(opts bot.Options) bot.Client {
		return client
	})

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	server.mu.Lock()
	server.runCtx = runCtx
	server.mu.Unlock()

	done := make(chan struct{})
	go func() {
		server.onInitialized(context.Background(), &sdkmcp.InitializedRequest{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("onInitialized blocked while bot login was in progress")
	}
	select {
	case <-client.loginStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("background login did not start")
	}
	close(client.releaseLogin)
	waitForRunCall(t, client, 1)
}

func TestBotOptionsNotifyLoginQRCode(t *testing.T) {
	t.Setenv("WECHAT_WIRE_DIR", t.TempDir())

	conn := &captureConnection{}
	server := NewServer("test", true, func(opts bot.Options) bot.Client {
		t.Fatal("factory should not be called")
		return nil
	})
	server.transport = &channelTransport{
		conn: &channelConnection{inner: conn, writeMu: &sync.Mutex{}},
	}
	server.mcpSession = &sdkmcp.ServerSession{}
	server.channelReady = true

	qrURL := "https://wechat.example/qr/login-token"
	server.botOptions().OnQRURL(qrURL)

	req := conn.onlyRequest(t)
	if req.Method != "notifications/claude/channel" {
		t.Fatalf("method: got %q want notifications/claude/channel", req.Method)
	}
	var params channelNotification
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if !stringsContainsAll(params.Content, "login required", qrURL) {
		t.Fatalf("content did not include login prompt and QR URL: %q", params.Content)
	}
	if params.Meta.EventType != "login_required" {
		t.Fatalf("event_type: got %q want login_required", params.Meta.EventType)
	}
	if params.Meta.MessageType != "login" {
		t.Fatalf("message_type: got %q want login", params.Meta.MessageType)
	}
}

func TestBotOptionsPreserveLoginProgressNotifications(t *testing.T) {
	t.Setenv("WECHAT_WIRE_DIR", t.TempDir())

	conn := &captureConnection{}
	server := NewServer("test", true, nil)
	server.transport = &channelTransport{
		conn: &channelConnection{inner: conn, writeMu: &sync.Mutex{}},
	}
	server.mcpSession = &sdkmcp.ServerSession{}
	server.channelReady = true

	options := server.botOptions()
	options.OnQRURL("https://wechat.example/qr/login-token")
	options.OnScanned()
	options.OnExpired()

	conn.mu.Lock()
	writes := append([]jsonrpc.Message(nil), conn.writes...)
	conn.mu.Unlock()
	if len(writes) != 3 {
		t.Fatalf("writes: got %d want 3", len(writes))
	}
	wantEvents := []string{"login_required", "login_scanned", "login_expired"}
	wantContents := []string{
		"wechat-wire login required. Scan this QR URL with WeChat to continue:\nhttps://wechat.example/qr/login-token",
		"wechat-wire login QR code scanned. Confirm the login in WeChat to continue.",
		"wechat-wire login QR code expired. Restart the MCP server to request a new QR URL.",
	}
	for i, msg := range writes {
		req, ok := msg.(*jsonrpc.Request)
		if !ok {
			t.Fatalf("write %d type: got %T want *jsonrpc.Request", i, msg)
		}
		var params channelNotification
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Fatalf("unmarshal write %d params: %v", i, err)
		}
		if params.Meta.EventType != wantEvents[i] {
			t.Fatalf("write %d event_type: got %q want %q", i, params.Meta.EventType, wantEvents[i])
		}
		if params.Content != wantContents[i] {
			t.Fatalf("write %d content: got %q want %q", i, params.Content, wantContents[i])
		}
	}
}

func TestFailedLoginReplacesStaleQRCodeStatus(t *testing.T) {
	t.Setenv("WECHAT_WIRE_DIR", t.TempDir())

	server := NewServer("test", true, nil)
	attempt := &botInitAttempt{done: make(chan struct{}), loginReady: make(chan struct{})}
	server.botInit = attempt
	server.loginState = loginState{
		EventType: "login_required",
		Content:   "https://wechat.example/qr/expired-token",
	}

	server.finishBotInit(attempt, nil, errors.New("QR code expired 3 times"))
	statusText := server.loginStatusText()
	if !stringsContainsAll(statusText, "login_state: login_failed", "QR code expired 3 times", "wechat_wire_login") {
		t.Fatalf("status did not replace stale QR after failure: %q", statusText)
	}
}

func TestLoginReturnsQRCodeWithoutWaitingForScan(t *testing.T) {
	t.Setenv("WECHAT_WIRE_DIR", t.TempDir())

	client := newBlockingClient()
	qrURL := "https://wechat.example/qr/login-token"
	server := NewServer("test", true, func(opts bot.Options) bot.Client {
		client.onLogin = func() { opts.OnQRURL(qrURL) }
		return client
	})
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	server.mu.Lock()
	server.runCtx = runCtx
	server.mu.Unlock()

	result, err := server.login(context.Background())
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !stringsContainsAll(result, "login_state: login_required", qrURL) {
		t.Fatalf("login result missing QR prompt: %q", result)
	}
	if got := client.loginCalls.Load(); got != 1 {
		t.Fatalf("login calls: got %d want 1", got)
	}
	select {
	case <-client.loginStarted:
	default:
		t.Fatal("login returned before the client started")
	}

	close(client.releaseLogin)
	waitForRunCall(t, client, 1)
	deadline := time.After(2 * time.Second)
	for {
		if got := server.loginStatusText(); strings.Contains(got, "login_state: logged_in") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("login state was not cleared after success: %q", server.loginStatusText())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestSendMessageNotificationIncludesMediaDownloadError(t *testing.T) {
	conn := &captureConnection{}
	server := NewServer("test", true, nil)
	server.transport = &channelTransport{
		conn: &channelConnection{inner: conn, writeMu: &sync.Mutex{}},
	}
	server.mcpSession = &sdkmcp.ServerSession{}
	server.channelReady = true

	server.sendMessageNotification(&bot.IncomingMessage{
		UserID:    "user-media",
		Text:      "[voice]",
		Type:      "voice",
		Timestamp: time.Unix(100, 0),
	}, nil, errors.New("cdn unavailable"))

	req := conn.onlyRequest(t)
	var params channelNotification
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if !stringsContainsAll(params.Content, "[voice]", "media_download_error: cdn unavailable") {
		t.Fatalf("content missing media failure details: %q", params.Content)
	}
	if params.Meta.EventType != "message" || params.Meta.MessageType != "voice" {
		t.Fatalf("unexpected meta: %+v", params.Meta)
	}
	if params.Meta.MediaType != "voice" || params.Meta.MediaDownloadError != "cdn unavailable" {
		t.Fatalf("unexpected media failure meta: %+v", params.Meta)
	}
}

func waitForRunCall(t *testing.T, client *blockingClient, want int32) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if client.runCalls.Load() == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("run calls: got %d want %d", client.runCalls.Load(), want)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func stringsContainsAll(s string, values ...string) bool {
	for _, value := range values {
		if !strings.Contains(s, value) {
			return false
		}
	}
	return true
}

type blockingClient struct {
	loginStarted chan struct{}
	releaseLogin chan struct{}
	onLogin      func()
	loginCalls   atomic.Int32
	runCalls     atomic.Int32
}

func newBlockingClient() *blockingClient {
	return &blockingClient{
		loginStarted: make(chan struct{}),
		releaseLogin: make(chan struct{}),
	}
}

func (c *blockingClient) Login(ctx context.Context, force bool) (*bot.Credentials, error) {
	if c.loginCalls.Add(1) == 1 {
		close(c.loginStarted)
	}
	if c.onLogin != nil {
		c.onLogin()
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.releaseLogin:
	}
	return &bot.Credentials{Token: "token", AccountID: "account", UserID: "bot"}, nil
}

func (c *blockingClient) OnMessage(handler func(*bot.IncomingMessage)) {}

func (c *blockingClient) Run(ctx context.Context) error {
	c.runCalls.Add(1)
	<-ctx.Done()
	return nil
}

func (c *blockingClient) Download(ctx context.Context, msg *bot.IncomingMessage) (*bot.DownloadedMedia, error) {
	return nil, fmt.Errorf("unexpected download")
}

func (c *blockingClient) Send(ctx context.Context, userID, text string) error {
	return c.SendWithContext(ctx, userID, text, "")
}

func (c *blockingClient) SendWithContext(ctx context.Context, userID, text, contextToken string) error {
	return fmt.Errorf("unexpected send")
}

func (c *blockingClient) SendAttachmentWithContext(ctx context.Context, userID string, attachment bot.OutboundAttachment, contextToken string) error {
	return fmt.Errorf("unexpected attachment send")
}

func (c *blockingClient) SendTyping(ctx context.Context, userID string) error {
	return fmt.Errorf("unexpected typing")
}

func (c *blockingClient) StopTyping(ctx context.Context, userID string) error {
	return fmt.Errorf("unexpected stop typing")
}

func (c *blockingClient) Stop() {}

type captureConnection struct {
	mu     sync.Mutex
	writes []jsonrpc.Message
}

func (c *captureConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *captureConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, msg)
	return nil
}

func (c *captureConnection) Close() error { return nil }

func (c *captureConnection) SessionID() string { return "test-session" }

func (c *captureConnection) onlyRequest(t *testing.T) *jsonrpc.Request {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.writes) != 1 {
		t.Fatalf("writes: got %d want 1", len(c.writes))
	}
	req, ok := c.writes[0].(*jsonrpc.Request)
	if !ok {
		t.Fatalf("write type: got %T want *jsonrpc.Request", c.writes[0])
	}
	return req
}
