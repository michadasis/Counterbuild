package app

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Live reads Riot's Live Client Data API, which the game serves on localhost
// with a self-signed certificate while a match is running.
type Live struct {
	url    string
	client *http.Client
}

func NewLive(url string) *Live {
	tr := &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 700 * time.Millisecond}).DialContext,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, // local game server, self-signed
		MaxIdleConns:        2,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 2 * time.Second,
	}
	return &Live{url: url, client: &http.Client{Transport: tr, Timeout: 2500 * time.Millisecond}}
}

// Fetch returns the raw allgamedata JSON, or an error when no game is running.
func (l *Live) Fetch() ([]byte, error) {
	resp, err := l.client.Get(l.url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("live client: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}
