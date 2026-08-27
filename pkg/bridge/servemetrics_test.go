package bridge

// Regression tests for ServeMetrics. It used to call log.Fatal (os.Exit)
// from a background goroutine on any listener error, meaning a metrics
// port bind conflict took down the entire bridge process, not just the
// metrics endpoint, bypassing any drain of in-flight work. It also never
// kept a reference to the server, so nothing could ever shut it down.

import (
	"context"
	"net"
	"testing"
	"time"

	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
)

func freeAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := l.Addr().String()

	require.NoError(t, l.Close())

	return addr
}

func TestServeMetrics_BindFailureDoesNotCrashProcess(t *testing.T) {
	// Occupy a port first so the metrics server's own bind fails.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	addr := listener.Addr().String()

	log, hook := logrustest.NewNullLogger()

	b := &Bridge{
		log: log,
		Cfg: Config{MetricsAddr: addr},
	}

	require.NoError(t, b.ServeMetrics(context.Background()))

	// If this test reaches this point at all, the process survived the
	// bind failure: the old code called log.Fatal (os.Exit) from the
	// background goroutine, which would have killed the whole test binary
	// before this assertion could ever run.
	require.Eventually(t, func() bool {
		for _, entry := range hook.AllEntries() {
			if entry.Message == "metrics server stopped unexpectedly" {
				return true
			}
		}

		return false
	}, 2*time.Second, 20*time.Millisecond, "expected the bind failure to be logged, not fatal")
}

func TestServeMetrics_ShutsDownCleanly(t *testing.T) {
	addr := freeAddr(t)

	log, _ := logrustest.NewNullLogger()

	b := &Bridge{
		log: log,
		Cfg: Config{MetricsAddr: addr},
	}

	require.NoError(t, b.ServeMetrics(context.Background()))

	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}

		conn.Close()

		return true
	}, 2*time.Second, 20*time.Millisecond, "expected the metrics server to start accepting connections")

	require.NotNil(t, b.metricsServer)
	require.NoError(t, b.metricsServer.Shutdown(context.Background()))

	// After a clean shutdown, new connections must be refused.
	_, err := net.Dial("tcp", addr)
	require.Error(t, err, "expected the metrics server to stop accepting connections after Shutdown")
}
