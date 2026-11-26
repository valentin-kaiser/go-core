package etcd_test

import (
	"context"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"go.etcd.io/etcd/server/v3/embed"
	"go.uber.org/zap"

	"github.com/valentin-kaiser/go-core/v2/apperror"
	"github.com/valentin-kaiser/go-core/v2/etcd"
)

const (
	loopbackHost        = "127.0.0.1"
	anyPortAddress      = loopbackHost + ":0"
	urlScheme           = "http://"
	dataDirName         = "data"
	serverReadyTimeout  = 30 * time.Second
	requestTimeout      = 5 * time.Second
	leaseTTL            = 5 * time.Second
	keyPrefixRoot       = "/test/"
	embeddedLogLevel    = "error"
	embeddedLoggerKind  = "zap"
	embeddedUnavailable = "embedded etcd unavailable: %v"
)

// startEmbedded starts a single node etcd server that is stopped and removed when the test ends.
func startEmbedded(tb testing.TB) []string {
	tb.Helper()

	cfg := embed.NewConfig()
	cfg.Dir = filepath.Join(tb.TempDir(), dataDirName)
	cfg.LogLevel = embeddedLogLevel
	cfg.Logger = embeddedLoggerKind
	cfg.ZapLoggerBuilder = embed.NewZapLoggerBuilder(zap.NewNop())

	clientPort, err := freePort()
	if err != nil {
		tb.Skipf(embeddedUnavailable, err)
	}
	peerPort, err := freePort()
	if err != nil {
		tb.Skipf(embeddedUnavailable, err)
	}

	clientURL, err := url.Parse(urlScheme + loopbackHost + ":" + strconv.Itoa(clientPort))
	if err != nil {
		tb.Fatalf("parsing client url failed: %v", err)
	}
	peerURL, err := url.Parse(urlScheme + loopbackHost + ":" + strconv.Itoa(peerPort))
	if err != nil {
		tb.Fatalf("parsing peer url failed: %v", err)
	}

	cfg.ListenClientUrls = []url.URL{*clientURL}
	cfg.AdvertiseClientUrls = []url.URL{*clientURL}
	cfg.ListenPeerUrls = []url.URL{*peerURL}
	cfg.AdvertisePeerUrls = []url.URL{*peerURL}
	cfg.InitialCluster = cfg.Name + "=" + peerURL.String()

	server, err := embed.StartEtcd(cfg)
	if err != nil {
		tb.Skipf(embeddedUnavailable, err)
	}
	tb.Cleanup(server.Close)

	select {
	case <-server.Server.ReadyNotify():
	case <-time.After(serverReadyTimeout):
		tb.Skipf(embeddedUnavailable, context.DeadlineExceeded)
	}

	return []string{clientURL.String()}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", anyPortAddress)
	if err != nil {
		return 0, err
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, apperror.NewError("listener address is not a TCP address")
	}
	err = l.Close()
	if err != nil {
		return 0, err
	}
	return addr.Port, nil
}

func newTestClient(tb testing.TB) *etcd.Client {
	tb.Helper()

	endpoints := startEmbedded(tb)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	cli, err := etcd.New(ctx, etcd.Config{
		Endpoints: endpoints,
		Prefix:    keyPrefixRoot + tb.Name(),
	})
	if err != nil {
		tb.Fatalf("etcd.New: %v", err)
	}
	tb.Cleanup(func() {
		err := cli.Close()
		if err != nil {
			tb.Errorf("closing client failed: %v", err)
		}
	})
	return cli
}
