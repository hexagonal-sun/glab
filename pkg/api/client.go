package api

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/profclems/glab/internal/config"
	"github.com/profclems/glab/internal/glinstance"
	"github.com/xanzy/go-gitlab"
)

// AuthType represents an authentication type within GitLab.
type authType int

const (
	NoToken authType = iota
	OAuthToken
	PrivateToken
)

const UserAgent = "GLab - GitLab CLI"

// Global api client to be used throughout glab
var a *Client

// TODO: remove this after replacing in all api files
var apiClient *gitlab.Client

// Client represents an argument to NewClient
type Client struct {
	// LabClient represents GitLab API client.
	// Note: this is exported for tests. Do not access it directly. Use Lab() method
	LabClient *gitlab.Client
	// internal http client
	httpClient *http.Client
	// internal http client overrider
	httpClientOverride *http.Client
	// Token type used to make authenticated API calls.
	AuthType authType
	// custom certificate
	caFile string
	// Protocol: host url protocol to make requests. Default is https
	Protocol string

	host  string
	token string

	isGraphQL          bool
	allowInsecure      bool
	refreshLabInstance bool
}

func init() {
	// initialise the global api client to be used throughout glab
	RefreshClient()
}

// RefreshClient re-initializes the api client
func RefreshClient()  {
	a = &Client{
		Protocol:           "https",
		AuthType:           NoToken,
		httpClient:         &http.Client{},
		refreshLabInstance: true,
	}
}

// GetAPIClient returns the global DotEnv instance.
func GetClient() *Client {
	return a
}

// HTTPClient returns the httpClient instance used to initialise the gitlab api client
func HTTPClient() *http.Client { return a.HTTPClient() }
func (c *Client) HTTPClient() *http.Client {
	if c.httpClientOverride != nil {
		return c.httpClientOverride
	}
	if c.httpClient != nil {
		return &http.Client{}
	}
	return c.httpClient
}

func (c *Client) OverrideHTTPClient(client *http.Client)  {
	c.httpClientOverride = client
}

// Token returns the authentication token
func Token() string { return a.Token() }
func (c *Client) Token() string {
	return c.token
}

func SetProtocol(protocol string) { a.SetProtocol(protocol) }
func (c *Client) SetProtocol(protocol string) {
	c.Protocol = protocol
}

// NewClient initializes a api client for use throughout glab.
func NewClient(host, token string, allowInsecure bool, isGraphQL bool) (*Client, error) {
	a.host = host
	a.token = token
	a.allowInsecure = allowInsecure
	a.isGraphQL = isGraphQL

	if a.httpClientOverride != nil {
		a.httpClient = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: a.allowInsecure,
				},
			},
		}
	}
	a.refreshLabInstance = true
	err := a.NewLab()
	return a, err
}

// NewClientWithCustomCA initializes the global api client with a self-signed certificate
func NewClientWithCustomCA(host, token, caFile string, isGraphQL bool) (*Client, error) {
	a.host = host
	a.token = token
	a.caFile = caFile
	a.isGraphQL = isGraphQL

	if a.httpClientOverride != nil {
		caCert, err := ioutil.ReadFile(a.caFile)
		if err != nil {
			return nil, fmt.Errorf("error reading cert file: %w", err)
		}
		// use system cert pool as a baseline
		caCertPool, err := x509.SystemCertPool()
		if err != nil {
			return nil, err
		}
		caCertPool.AppendCertsFromPEM(caCert)

		a.httpClient = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				TLSClientConfig: &tls.Config{
					RootCAs: caCertPool,
				},
			},
		}
	}
	a.refreshLabInstance = true
	err := a.NewLab()
	return a, err
}

// NewClientWithCfg initializes the global api with the config data
func NewClientWithCfg(repoHost string, cfg config.Config, isGraphQL bool) (client *Client, err error) {
	if repoHost == "" {
		repoHost = glinstance.OverridableDefault()
	}
	token, _ := cfg.Get(repoHost, "token")
	tlsVerify, _ := cfg.Get(repoHost, "skip_tls_verify")
	skipTlsVerify := tlsVerify == "true" || tlsVerify == "1"
	caCert, _ := cfg.Get(repoHost, "ca_cert")
	if caCert != "" {
		client, err = NewClientWithCustomCA(repoHost, token, caCert, isGraphQL)
	} else {
		client, err = NewClient(repoHost, token, skipTlsVerify, isGraphQL)
	}
	if err != nil {
		return
	}
	return
}

// NewLab initializes the GitLab Client
func (c *Client) NewLab() error {
	var err error
	var baseURL string
	httpClient := c.httpClient

	if c.httpClientOverride != nil {
		httpClient = c.httpClientOverride
	}
	if a.refreshLabInstance {
		if c.host == "" {
			c.host = glinstance.OverridableDefault()
		}
		if c.isGraphQL {
			baseURL = glinstance.GraphQLEndpoint(c.host, c.Protocol)
		} else {
			baseURL = glinstance.APIEndpoint(c.host, c.Protocol)
		}
		c.LabClient, err = gitlab.NewClient(c.token, gitlab.WithHTTPClient(httpClient), gitlab.WithBaseURL(baseURL))
		if err != nil {
			return fmt.Errorf("failed to initialize GitLab client: %v", err)
		}
		c.LabClient.UserAgent = UserAgent

		apiClient = c.LabClient
		if c.token != "" {
			c.AuthType = PrivateToken
		}
	}
	return nil
}

// Lab returns the initialized GitLab client.
// Initializes a new GitLab Client if not initialized but error is ignored
func (c *Client) Lab() *gitlab.Client {
	if c.LabClient != nil {
		return c.LabClient
	}
	err := c.NewLab()
	if err != nil {
		c.LabClient = &gitlab.Client{}
	}
	return c.LabClient
}

// BaseURL returns a copy of the BaseURL
func (c *Client) BaseURL() *url.URL {
	return c.Lab().BaseURL()
}

func TestClient(httpClient *http.Client, token, host string, isGraphQL bool) (*Client, error) {
	testClient, err := NewClient(host, token, true, isGraphQL)
	if err != nil {
		return nil, err
	}
	testClient.SetProtocol("https")
	testClient.OverrideHTTPClient(httpClient)
	if token != "" {
		a.AuthType = PrivateToken
	}
	return testClient, nil
}
