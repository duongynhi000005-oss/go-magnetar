package requester_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmentor/go-magnetar/internal/config"
	"github.com/wmentor/go-magnetar/internal/http/requester"
)

func TestGet(t *testing.T) {
	for range 10 {
		client := requester.Get()
		require.NotNil(t, client)
	}
}

func TestNewFactory(t *testing.T) {
	factory := requester.NewFactory()
	require.NotNil(t, factory)

	// Verify that factory implements ClientFactory interface
	var _ requester.ClientFactory = factory
}

func TestFactory_Get(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")
	requester.Configure("", "", "")

	factory := requester.NewFactory()

	// Test that Get() returns a non-nil client
	client := factory.Get()
	require.NotNil(t, client)

	// Test that the returned client can perform HTTP requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestFactory_Put(t *testing.T) {
	factory := requester.NewFactory()

	// Test that Put() does not panic
	client := factory.Get()
	require.NotPanics(t, func() {
		factory.Put(client)
	})

	// Test that Put() can be called multiple times
	require.NotPanics(t, func() {
		factory.Put(client)
		factory.Put(nil)
	})
}

func TestProxySupport(t *testing.T) {
	var proxyHits atomic.Int32
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyHits.Add(1)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("proxied"))
	}))
	defer proxySrv.Close()

	var envProxyHits atomic.Int32
	envProxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		envProxyHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer envProxySrv.Close()

	t.Cleanup(func() {
		requester.Configure("", "", "")
	})

	t.Run("environment variable HTTP_PROXY routes traffic through proxy", func(t *testing.T) {
		proxyHits.Store(0)
		t.Setenv("HTTP_PROXY", proxySrv.URL)
		t.Setenv("NO_PROXY", "")
		requester.Configure("", "", "")

		req, err := http.NewRequest(http.MethodGet, "http://example.com/resource", nil)
		require.NoError(t, err)

		resp, err := requester.Get().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusAccepted, resp.StatusCode)
		require.Equal(t, int32(1), proxyHits.Load())
	})

	t.Run("config values take priority over environment variables", func(t *testing.T) {
		proxyHits.Store(0)
		envProxyHits.Store(0)
		t.Setenv("HTTP_PROXY", envProxySrv.URL)
		t.Setenv("NO_PROXY", "")

		cfgPath := filepath.Join(t.TempDir(), "config.yml")
		yaml := "version: \"1.0\"\nproxy:\n  http: " + proxySrv.URL + "\n"
		require.NoError(t, os.WriteFile(cfgPath, []byte(yaml), 0o600))

		cfg, err := config.Load(cfgPath)
		require.NoError(t, err)

		requester.ConfigureFromConfig(cfg)

		req, err := http.NewRequest(http.MethodGet, "http://example.com/priority", nil)
		require.NoError(t, err)

		resp, err := requester.Get().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusAccepted, resp.StatusCode)
		require.Equal(t, int32(1), proxyHits.Load())
		require.Equal(t, int32(0), envProxyHits.Load())
	})

	t.Run("NO_PROXY bypasses proxy for matching hosts", func(t *testing.T) {
		targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer targetSrv.Close()

		proxyHits.Store(0)
		requester.Configure(proxySrv.URL, "", "127.0.0.1,localhost")

		req, err := http.NewRequest(http.MethodGet, targetSrv.URL, nil)
		require.NoError(t, err)

		resp, err := requester.Get().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, int32(0), proxyHits.Load())
	})

	t.Run("invalid proxy URL falls back gracefully to direct connection", func(t *testing.T) {
		targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer targetSrv.Close()

		t.Setenv("HTTP_PROXY", "")
		requester.Configure("://bad-proxy-url", "not-a-valid-url", "")

		req, err := http.NewRequest(http.MethodGet, targetSrv.URL, nil)
		require.NoError(t, err)

		resp, err := requester.Get().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
	})
}
