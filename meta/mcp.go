// Package meta fetches community build data (what players most often build)
// and turns it into item popularity for the engine.
package meta

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// mcpClient is a minimal Model Context Protocol client over Streamable HTTP:
// JSON-RPC requests POSTed to one URL, answered as JSON or as server-sent events.
type mcpClient struct {
	url     string
	http    *http.Client
	session string
	proto   string
	nextID  int
	agent   string
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e *rpcError) Error() string {
	if len(e.Data) > 0 {
		return fmt.Sprintf("rpc %d: %s (%s)", e.Code, e.Message, truncate(string(e.Data), 300))
	}
	return fmt.Sprintf("rpc %d: %s", e.Code, e.Message)
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func (c *mcpClient) post(ctx context.Context, payload map[string]any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", c.agent)
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	if c.proto != "" {
		req.Header.Set("MCP-Protocol-Version", c.proto)
	}
	return c.http.Do(req)
}

// call sends a request and waits for its response.
func (c *mcpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.nextID++
	id := c.nextID
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	resp, err := c.post(ctx, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.session = sid
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("%s: HTTP %d %s", method, resp.StatusCode, truncate(strings.TrimSpace(string(b)), 300))
	}
	var msgs []rpcResponse
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		msgs, err = readSSE(resp.Body)
	} else {
		var b []byte
		b, err = io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err == nil {
			msgs, err = decodeMessages(b)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	want := fmt.Sprint(id)
	for _, m := range msgs {
		if strings.Trim(string(m.ID), `"`) != want {
			continue
		}
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	}
	return nil, fmt.Errorf("%s: no response with id %d", method, id)
}

// notify sends a notification (no response expected).
func (c *mcpClient) notify(ctx context.Context, method string) {
	resp, err := c.post(ctx, map[string]any{"jsonrpc": "2.0", "method": method})
	if err == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}
}

// close ends the session politely; failures are ignored.
func (c *mcpClient) close(ctx context.Context) {
	if c.session == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", c.session)
	req.Header.Set("User-Agent", c.agent)
	if c.proto != "" {
		req.Header.Set("MCP-Protocol-Version", c.proto)
	}
	if resp, err := c.http.Do(req); err == nil {
		resp.Body.Close()
	}
}

func decodeMessages(b []byte) ([]rpcResponse, error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, errors.New("empty response")
	}
	if b[0] == '[' {
		var arr []rpcResponse
		err := json.Unmarshal(b, &arr)
		return arr, err
	}
	var one rpcResponse
	if err := json.Unmarshal(b, &one); err != nil {
		return nil, err
	}
	return []rpcResponse{one}, nil
}

// readSSE parses a text/event-stream body into JSON-RPC messages.
func readSSE(r io.Reader) ([]rpcResponse, error) {
	sc := bufio.NewScanner(io.LimitReader(r, 16<<20))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var out []rpcResponse
	var data []string
	flush := func() {
		if len(data) == 0 {
			return
		}
		if msgs, err := decodeMessages([]byte(strings.Join(data, "\n"))); err == nil {
			out = append(out, msgs...)
		}
		data = data[:0]
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	if err := sc.Err(); err != nil && len(out) == 0 {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("no messages in event stream")
	}
	return out, nil
}
