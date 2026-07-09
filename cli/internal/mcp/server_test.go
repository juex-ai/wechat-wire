package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

type blockingClient struct {
	loginStarted chan struct{}
	releaseLogin chan struct{}
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

func (c *blockingClient) Send(ctx context.Context, userID, text string) error {
	return c.SendWithContext(ctx, userID, text, "")
}

func (c *blockingClient) SendWithContext(ctx context.Context, userID, text, contextToken string) error {
	return fmt.Errorf("unexpected send")
}

func (c *blockingClient) SendTyping(ctx context.Context, userID string) error {
	return fmt.Errorf("unexpected typing")
}

func (c *blockingClient) StopTyping(ctx context.Context, userID string) error {
	return fmt.Errorf("unexpected stop typing")
}

func (c *blockingClient) Stop() {}
