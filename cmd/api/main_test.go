package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// start runs serve on a free port with a handler that takes `work` to answer.
// get closes the body; it returns the error only, which is all these tests look at.
func get(url string) error {
	res, err := http.Get(url)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

func start(t *testing.T, work time.Duration, plan shutdownPlan) (url string, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(work)
		_, _ = io.WriteString(w, "done")
	})}
	plan.logger = zerolog.Nop()
	if plan.drain == nil {
		plan.drain = func() {}
	}
	result := make(chan error, 1)
	go func() { result <- serve(ctx, func() {}, srv, ln, plan) }()
	return "http://" + ln.Addr().String(), cancel, result
}

// A request that is running when the signal arrives is finished, not cut off.
func TestServe_FinishesRunningRequests(t *testing.T) {
	url, cancel, done := start(t, 300*time.Millisecond, shutdownPlan{timeout: 5 * time.Second})

	answer := make(chan string, 1)
	go func() {
		res, err := http.Get(url)
		if err != nil {
			answer <- "error: " + err.Error()
			return
		}
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(res.Body)
		answer <- string(body)
	}()
	time.Sleep(100 * time.Millisecond) // the request is now inside the handler
	cancel()

	assert.Equal(t, "done", <-answer, "the running request got its answer")
	assert.NoError(t, <-done, "a clean shutdown is not an error")

	assert.Error(t, get(url), "nothing listens any more")
}

// Health fails first (drain), and requests are still served during the drain delay.
func TestServe_DrainsBeforeStopping(t *testing.T) {
	var draining atomic.Bool
	url, cancel, done := start(t, 0, shutdownPlan{
		timeout: 5 * time.Second, drainDelay: 300 * time.Millisecond, drain: func() { draining.Store(true) },
	})

	cancel()
	require.Eventually(t, draining.Load, time.Second, 10*time.Millisecond, "the drain starts at once")
	require.NoError(t, get(url), "still accepting connections during the drain delay")

	assert.NoError(t, <-done)
	assert.Error(t, get(url))
}

// A request that outlasts the timeout is cut off, and the caller learns about it.
func TestServe_ForcesCloseAfterTimeout(t *testing.T) {
	url, cancel, done := start(t, 2*time.Second, shutdownPlan{timeout: 200 * time.Millisecond})

	go func() { _ = get(url) }()
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not return after the shutdown timeout")
	}
}
