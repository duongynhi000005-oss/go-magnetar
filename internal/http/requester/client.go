package requester

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"

	"github.com/wmentor/go-magnetar/internal/config"
	"github.com/wmentor/go-magnetar/internal/printer"
)

const (
	maxTimeout = time.Hour * 2
)

type ClientFactory interface {
	Get() Client
	Put(c Client)
}

type Client interface {
	Do(req *http.Request) (*http.Response, error)
}

type factory struct{}

var (
	agent   *http.Client
	proxyMu sync.RWMutex
	proxyFn func(*url.URL) (*url.URL, error)
)

func init() {
	Configure("", "", "")

	agent = NewHTTPClient(maxTimeout)
}

// NewTransport returns a new http.Transport configured with proxy support and TLS settings.
func NewTransport() *http.Transport {
	return &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		DisableKeepAlives: true,
		Proxy:             resolveProxy,
	}
}

// NewHTTPClient returns a new http.Client with the specified timeout and proxy-enabled transport.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: NewTransport(),
		Timeout:   timeout,
	}
}

func resolveProxy(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil //nolint:nilnil // http.Transport.Proxy contract returns (nil, nil) for direct connection.
	}

	proxyMu.RLock()
	fn := proxyFn
	proxyMu.RUnlock()

	if fn == nil {
		return nil, nil //nolint:nilnil // http.Transport.Proxy contract returns (nil, nil) for direct connection.
	}

	u, err := fn(req.URL)
	if err != nil {
		printer.Warn("http: invalid proxy configuration, continuing without proxy", "err", err)
		return nil, nil //nolint:nilnil // graceful fallback without proxy when proxy resolution fails.
	}

	return u, nil
}

func validateProxyURL(name, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		printer.Warn("http: invalid proxy URL, ignoring", "setting", name, "value", trimmed)
		return ""
	}

	return trimmed
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

// Configure sets the proxy configuration with priority: explicit arguments > environment variables.
// Invalid proxy URLs log a warning and fall back to no proxy.
func Configure(httpProxy, httpsProxy, noProxy string) {
	envCfg := httpproxy.FromEnvironment()

	rawHTTP := firstNonEmpty(httpProxy, envCfg.HTTPProxy)
	rawHTTPS := firstNonEmpty(httpsProxy, envCfg.HTTPSProxy)
	rawNoProxy := firstNonEmpty(noProxy, envCfg.NoProxy)

	cfg := &httpproxy.Config{
		HTTPProxy:  validateProxyURL("HTTP_PROXY", rawHTTP),
		HTTPSProxy: validateProxyURL("HTTPS_PROXY", rawHTTPS),
		NoProxy:    strings.TrimSpace(rawNoProxy),
		CGI:        envCfg.CGI,
	}

	fn := cfg.ProxyFunc()

	proxyMu.Lock()
	proxyFn = fn
	proxyMu.Unlock()
}

// ConfigureFromConfig applies proxy settings from Config (falling back to environment variables).
func ConfigureFromConfig(cfg *config.Config) {
	if cfg == nil {
		Configure("", "", "")
		return
	}

	httpProxy := firstNonEmpty(cfg.String("proxy.http"), cfg.String("http_proxy"))
	httpsProxy := firstNonEmpty(cfg.String("proxy.https"), cfg.String("https_proxy"))
	noProxy := firstNonEmpty(cfg.String("proxy.no_proxy"), cfg.String("no_proxy"))

	Configure(httpProxy, httpsProxy, noProxy)
}

func Get() Client {
	return agent
}

func NewFactory() ClientFactory {
	return &factory{}
}

func (f *factory) Get() Client {
	return agent
}

func (f *factory) Put(_ Client) {
}
