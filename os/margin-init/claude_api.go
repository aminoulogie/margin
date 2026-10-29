package main

// The Claude tab's connection to Anthropic's Messages API: one streamed request per turn
// (server-sent events), text shown as it arrives, tool calls collected whole.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// claudeAPI is the Messages API endpoint; MARGIN_CLAUDE_API points tests at a fake server.
func claudeAPI() string {
	if u := os.Getenv("MARGIN_CLAUDE_API"); u != "" {
		return u
	}
	return "https://api.anthropic.com/v1/messages"
}

// cMsg and cBlock are the API's messages and content blocks, kept as the conversation.
type cMsg struct {
	Role    string   `json:"role"`
	Content []cBlock `json:"content"`
}

type cBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	ServerName   string          `json:"server_name,omitempty"` // mcp_tool_use: which connector
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      string          `json:"content,omitempty"`
	IsError      bool            `json:"is_error,omitempty"`
	CacheControl *cacheCtl       `json:"cache_control,omitempty"`

	Raw json.RawMessage `json:"-"` // a block kept whole, as the API sent it (mcp_tool_result)
}

// A connector's result (mcp_tool_result) has a list as its content: it's kept as it came and
// sent back the same in the next requests.
func (b *cBlock) UnmarshalJSON(d []byte) error {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(d, &head); err != nil {
		return err
	}
	if head.Type == "mcp_tool_result" {
		*b = cBlock{Type: head.Type, Raw: append(json.RawMessage(nil), d...)}
		return nil
	}
	type plain cBlock
	return json.Unmarshal(d, (*plain)(b))
}

func (b cBlock) MarshalJSON() ([]byte, error) {
	if b.Raw != nil {
		return b.Raw, nil
	}
	type plain cBlock
	return json.Marshal(plain(b))
}

// mcpResult is a connector result's text and whether it's an error.
func mcpResult(raw json.RawMessage) (string, bool) {
	var r struct {
		IsError bool            `json:"is_error"`
		Content json.RawMessage `json:"content"`
	}
	json.Unmarshal(raw, &r)
	var parts []struct {
		Type, Text string
	}
	if json.Unmarshal(r.Content, &parts) != nil {
		var s string
		json.Unmarshal(r.Content, &s)
		return s, r.IsError
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String(), r.IsError
}

type cacheCtl struct {
	Type string `json:"type"`
}

// cTool is a tool Claude may call: one of Margin's (name, description, schema) or a
// connector's whole tool set (type mcp_toolset).
type cTool struct {
	Type          string          `json:"type,omitempty"`
	Name          string          `json:"name,omitempty"`
	Description   string          `json:"description,omitempty"`
	Schema        json.RawMessage `json:"input_schema,omitempty"`
	MCPServerName string          `json:"mcp_server_name,omitempty"`
}

// cMCPServer is a connector Anthropic's servers connect to for Claude (the MCP connector).
type cMCPServer struct {
	Type  string `json:"type"` // "url"
	URL   string `json:"url"`
	Name  string `json:"name"`
	Token string `json:"authorization_token,omitempty"`
}

type cRequest struct {
	Model      string       `json:"model"`
	MaxTokens  int          `json:"max_tokens"`
	System     []cBlock     `json:"system"`
	Messages   []cMsg       `json:"messages"`
	Tools      []cTool      `json:"tools"`
	MCPServers []cMCPServer `json:"mcp_servers,omitempty"`
	Stream     bool         `json:"stream"`
}

// mcpBeta is the beta the MCP connector (mcp_servers) needs.
const mcpBeta = "mcp-client-2025-11-20"

// withConnectors adds the connectors to a request: each server, and its tool set.
func (r *cRequest) withConnectors(cs []claudeConnector) {
	for _, c := range cs {
		r.MCPServers = append(r.MCPServers, cMCPServer{Type: "url", URL: c.URL, Name: c.Name, Token: c.Token})
		r.Tools = append(r.Tools, cTool{Type: "mcp_toolset", MCPServerName: c.Name})
	}
}

// cReply is what one streamed answer came to.
type cReply struct {
	Blocks     []cBlock
	StopReason string
	InTokens   int
	OutTokens  int
}

// apiError is an error the API answered with (status and its message), as opposed to the
// network failing.
type apiError struct {
	Status  int
	Kind    string
	Message string
}

func (e *apiError) Error() string {
	switch {
	case e.Status == 401:
		return "the API key was refused (type /key to change it)"
	case e.Status == 400 && strings.Contains(e.Message, "too long"):
		return "the conversation is too long for the model now: type /clear to start a new one"
	case e.Status == 400 && strings.Contains(strings.ToLower(e.Message), "credit"):
		return "no API credit left: " + e.Message
	}
	return fmt.Sprintf("API error %d %s: %s", e.Status, e.Kind, e.Message)
}

func (e *apiError) retryable() bool {
	return e.Status == 429 || e.Status == 500 || e.Status == 502 || e.Status == 503 || e.Status == 529 || e.Kind == "overloaded_error"
}

// claudeNet sends requests through margin-init's proxy first (as everything in Alpine does),
// and straight out over Wi-Fi when the proxy can't connect, remembering what worked.
type claudeNet struct {
	viaProxy, direct *http.Client
	useDirect        bool
}

func newClaudeNet() *claudeNet {
	mk := func(proxy func(*http.Request) (*url.URL, error)) *http.Client {
		return &http.Client{Transport: &http.Transport{
			Proxy:                 proxy,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: 5 * time.Minute,
			IdleConnTimeout:       90 * time.Second,
		}}
	}
	return &claudeNet{viaProxy: mk(http.ProxyFromEnvironment), direct: mk(nil)}
}

func (n *claudeNet) do(req func() (*http.Request, error)) (*http.Response, error) {
	order := []*http.Client{n.viaProxy, n.direct}
	if n.useDirect {
		order = []*http.Client{n.direct, n.viaProxy}
	}
	var lastErr error
	for _, cl := range order {
		r, err := req()
		if err != nil {
			return nil, err
		}
		resp, err := cl.Do(r)
		if err == nil {
			n.useDirect = cl == n.direct
			return resp, nil
		}
		if r.Context().Err() != nil {
			return nil, r.Context().Err()
		}
		lastErr = err
	}
	return nil, lastErr
}

// stream sends one request and streams the answer: onText gets text as it arrives, onTool
// the name of each tool call as it starts (so the screen can say what's coming).
func (n *claudeNet) stream(ctx context.Context, key string, body cRequest, onText func(string), onTool func(string), onDone func(cBlock)) (*cReply, error) {
	body.Stream = true
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	resp, err := n.do(func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, "POST", claudeAPI(), bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		r.Header.Set("content-type", "application/json")
		r.Header.Set("x-api-key", key)
		r.Header.Set("anthropic-version", "2023-06-01")
		if len(body.MCPServers) > 0 {
			r.Header.Set("anthropic-beta", mcpBeta)
		}
		return r, nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var e struct {
			Error struct{ Type, Message string } `json:"error"`
		}
		json.Unmarshal(b, &e)
		if e.Error.Message == "" {
			e.Error.Message = strings.TrimSpace(string(b))
		}
		return nil, &apiError{resp.StatusCode, e.Error.Type, e.Error.Message}
	}
	return readStream(resp.Body, onText, onTool, onDone)
}

// readStream turns the server-sent events of a streamed answer into its blocks. onTool gets
// the name of each tool call as it starts; onDone each connector call (mcp_tool_use) and
// result (mcp_tool_result) once whole, to be shown (Anthropic's servers make those calls).
func readStream(r io.Reader, onText func(string), onTool func(string), onDone func(cBlock)) (*cReply, error) {
	rep := &cReply{}
	var partial []strings.Builder // tool inputs as their JSON arrives, by block index
	var startIn []json.RawMessage // a tool input given whole at the start
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var ev struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock cBlock `json:"content_block"`
			Delta        struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Message struct {
				Usage struct {
					InputTokens         int `json:"input_tokens"`
					CacheReadTokens     int `json:"cache_read_input_tokens"`
					CacheCreationTokens int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error struct{ Type, Message string } `json:"error"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "message_start":
			u := ev.Message.Usage
			rep.InTokens = u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens
		case "content_block_start":
			for len(rep.Blocks) <= ev.Index {
				rep.Blocks = append(rep.Blocks, cBlock{})
				partial = append(partial, strings.Builder{})
				startIn = append(startIn, nil)
			}
			b := ev.ContentBlock
			startIn[ev.Index], b.Input = b.Input, nil
			rep.Blocks[ev.Index] = b
			if (b.Type == "tool_use" || b.Type == "mcp_tool_use") && onTool != nil {
				onTool(b.Name)
			}
			if b.Type == "mcp_tool_result" && onDone != nil {
				onDone(b)
			}
			if b.Type == "text" && b.Text != "" && onText != nil {
				onText(b.Text)
			}
		case "content_block_delta":
			if ev.Index >= len(rep.Blocks) {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				rep.Blocks[ev.Index].Text += ev.Delta.Text
				if onText != nil {
					onText(ev.Delta.Text)
				}
			case "input_json_delta":
				partial[ev.Index].WriteString(ev.Delta.PartialJSON)
			}
		case "content_block_stop":
			if ev.Index < len(rep.Blocks) && (rep.Blocks[ev.Index].Type == "tool_use" || rep.Blocks[ev.Index].Type == "mcp_tool_use") {
				in := strings.TrimSpace(partial[ev.Index].String())
				if in == "" {
					in = strings.TrimSpace(string(startIn[ev.Index]))
				}
				if in == "" {
					in = "{}"
				}
				rep.Blocks[ev.Index].Input = json.RawMessage(in)
				if rep.Blocks[ev.Index].Type == "mcp_tool_use" && onDone != nil {
					onDone(rep.Blocks[ev.Index])
				}
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				rep.StopReason = ev.Delta.StopReason
			}
			rep.OutTokens = ev.Usage.OutputTokens
		case "error":
			return rep, &apiError{Status: 529, Kind: ev.Error.Type, Message: ev.Error.Message}
		case "message_stop":
			return rep, nil
		}
	}
	if err := sc.Err(); err != nil {
		return rep, err
	}
	return rep, errors.New("the answer stopped before it was finished (connection dropped)")
}
