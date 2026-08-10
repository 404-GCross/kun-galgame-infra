package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gorm.io/datatypes"
)

func clientWithRedirects(json string) *OAuthClient {
	return &OAuthClient{RedirectURIs: datatypes.JSON([]byte(json))}
}

func TestHasRedirectURIExactMatch(t *testing.T) {
	c := clientWithRedirects(`["https://app.example.com/cb","https://app.example.com/alt"]`)

	assert.True(t, c.HasRedirectURI("https://app.example.com/cb"))
	assert.True(t, c.HasRedirectURI("https://app.example.com/alt"))

	assert.False(t, c.HasRedirectURI("https://app.example.com/cb2"))
	assert.False(t, c.HasRedirectURI("https://evil.example.com/cb"))
	assert.False(t, c.HasRedirectURI("http://app.example.com/cb"))
	assert.False(t, c.HasRedirectURI("https://app.example.com:8443/cb"))
	assert.False(t, c.HasRedirectURI(""))
}

func TestHasRedirectURILoopbackIgnoresPort(t *testing.T) {
	c := clientWithRedirects(`["http://127.0.0.1:53682/callback"]`)

	assert.True(t, c.HasRedirectURI("http://127.0.0.1:53682/callback"), "the registered port")
	assert.True(t, c.HasRedirectURI("http://127.0.0.1:61111/callback"), "an ephemeral port")
	assert.True(t, c.HasRedirectURI("http://127.0.0.1/callback"), "no port at all")

	assert.False(t, c.HasRedirectURI("http://127.0.0.1:61111/other"), "a different path")
	assert.False(t, c.HasRedirectURI("https://127.0.0.1:53682/callback"), "a different scheme")
	assert.False(t, c.HasRedirectURI("http://127.0.0.2:53682/callback"), "a different address")
	assert.False(t, c.HasRedirectURI("http://evil.example.com/callback"), "a real host")
	assert.False(t, c.HasRedirectURI("http://localhost:53682/callback"), "localhost by name")
}

func TestHasRedirectURILoopbackIPv6(t *testing.T) {
	c := clientWithRedirects(`["http://[::1]:53682/callback"]`)
	assert.True(t, c.HasRedirectURI("http://[::1]:9999/callback"))
	assert.False(t, c.HasRedirectURI("http://127.0.0.1:9999/callback"), "v4 and v6 loopback are different registrations")
}

func TestHasRedirectURIMixedListKeepsWebExact(t *testing.T) {
	c := clientWithRedirects(`["http://127.0.0.1:1/cb","https://app.example.com/cb"]`)
	assert.True(t, c.HasRedirectURI("http://127.0.0.1:65000/cb"))
	assert.False(t, c.HasRedirectURI("https://app.example.com:8443/cb"))
}

func TestHasRedirectURIEmptyOrBrokenList(t *testing.T) {
	assert.False(t, clientWithRedirects(`[]`).HasRedirectURI("https://app.example.com/cb"),
		"a client with no registered callback redirects nowhere")
	assert.False(t, clientWithRedirects(`not json`).HasRedirectURI("https://app.example.com/cb"),
		"an unreadable list fails closed")
}
