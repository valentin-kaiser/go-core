package etcd_test

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.etcd.io/etcd/server/v3/embed"
	"go.uber.org/zap"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/etcd"
)

// testEtcd holds a singleton embedded etcd instance shared by every test in
// this package to keep CI fast and avoid port pressure.
type testEtcd struct {
	endpoints []string
	dir       string
	server    *embed.Etcd
}

var (
	sharedOnce   sync.Once
	sharedServer *testEtcd
	errShared    error
)

func sharedEtcd(tb testing.TB) []string {
	tb.Helper()
	sharedOnce.Do(func() {
		sharedServer, errShared = startEmbedded()
	})
	if errShared != nil {
		tb.Skipf("embedded etcd unavailable: %v", errShared)
	}
	return sharedServer.endpoints
}

func startEmbedded() (*testEtcd, error) {
	dir, err := os.MkdirTemp("", "go-core-etcd-*")
	if err != nil {
		return nil, err
	}

	cfg := embed.NewConfig()
	cfg.Dir = filepath.Join(dir, "data")
	cfg.LogLevel = "error"
	cfg.Logger = "zap"
	cfg.ZapLoggerBuilder = embed.NewZapLoggerBuilder(zap.NewNop())

	clientPort, err := freePort()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	peerPort, err := freePort()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	clientURL, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(clientPort))
	peerURL, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(peerPort))

	cfg.ListenClientUrls = []url.URL{*clientURL}
	cfg.AdvertiseClientUrls = []url.URL{*clientURL}
	cfg.ListenPeerUrls = []url.URL{*peerURL}
	cfg.AdvertisePeerUrls = []url.URL{*peerURL}
	cfg.InitialCluster = cfg.Name + "=" + peerURL.String()

	server, err := embed.StartEtcd(cfg)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	select {
	case <-server.Server.ReadyNotify():
	case <-time.After(30 * time.Second):
		server.Close()
		_ = os.RemoveAll(dir)
		return nil, context.DeadlineExceeded
	}

	return &testEtcd{
		endpoints: []string{clientURL.String()},
		dir:       dir,
		server:    server,
	}, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, apperror.NewError("listener address is not a TCP address")
	}
	return addr.Port, nil
}

func newTestClient(tb testing.TB) *etcd.Client {
	tb.Helper()
	endpoints := sharedEtcd(tb)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := etcd.New(ctx, etcd.Config{
		Endpoints: endpoints,
		Prefix:    "/test/" + tb.Name(),
	})
	if err != nil {
		tb.Fatalf("etcd.New: %v", err)
	}
	tb.Cleanup(func() { _ = cli.Close() })
	return cli
}
