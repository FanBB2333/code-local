package auth

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
)

type Client struct {
	BaseURL    string
	Password   string
	HTTPClient *http.Client
	SessionCookie *http.Cookie
}

func NewClient(baseURL, password string) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}

	// Don't follow redirects — we need the Set-Cookie header from the 302
	httpClient := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Password:   password,
		HTTPClient: httpClient,
	}, nil
}

// Login authenticates with code-server and stores the session cookie.
func (c *Client) Login() error {
	loginURL := c.BaseURL + "/login"

	form := url.Values{}
	form.Set("password", c.Password)

	resp, err := c.HTTPClient.Post(loginURL, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	// code-server returns 302 on success, 200 on failure (with error page)
	if resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(body), "Incorrect password") ||
			strings.Contains(string(body), "error") {
			return fmt.Errorf("login failed: incorrect password")
		}
		return fmt.Errorf("login failed: unexpected 200 response")
	}

	if resp.StatusCode != http.StatusFound {
		return fmt.Errorf("login failed: unexpected status %d", resp.StatusCode)
	}

	// Extract session cookie
	parsedURL, err := url.Parse(c.BaseURL)
	if err != nil {
		return fmt.Errorf("parse base URL: %w", err)
	}

	for _, cookie := range c.HTTPClient.Jar.Cookies(parsedURL) {
		if strings.HasPrefix(cookie.Name, "code-server-session") {
			c.SessionCookie = cookie
			return nil
		}
	}

	// Also check raw Set-Cookie headers
	for _, setCookie := range resp.Header["Set-Cookie"] {
		if strings.Contains(setCookie, "code-server-session") {
			header := http.Header{}
			header.Add("Set-Cookie", setCookie)
			resp2 := &http.Response{Header: header}
			cookies := resp2.Cookies()
			if len(cookies) > 0 {
				c.SessionCookie = cookies[0]
				return nil
			}
		}
	}

	return fmt.Errorf("login succeeded but no session cookie found")
}

// CookieHeader returns the cookie string for use in WebSocket upgrade requests.
func (c *Client) CookieHeader() string {
	if c.SessionCookie == nil {
		return ""
	}
	return c.SessionCookie.Name + "=" + c.SessionCookie.Value
}

// Origin returns the origin string derived from the base URL.
func (c *Client) Origin() string {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return c.BaseURL
	}
	return u.Scheme + "://" + u.Host
}

// WebSocketURL returns the WebSocket URL for connecting to VS Code Server.
func (c *Client) WebSocketURL() (string, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}

	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}

	return u.String(), nil
}
