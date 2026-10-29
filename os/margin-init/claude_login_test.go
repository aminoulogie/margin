package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"margin-init/vt"
)

// claudeCodeScreen writes a sign-in screen the way Claude Code 2.1.112 does (checked against
// its real output): spaces as cursor-right moves, the link cut into rows as wide as the
// terminal, each row in a colour, rows ended by \r\r\n.
func claudeCodeScreen(url string, cols int) string {
	var sb strings.Builder
	sb.WriteString("\x1b[1C\x1b[1A\x1b[38;5;246mBrowser\x1b[1Cdidn't\x1b[1Copen?\x1b[1CUse\x1b[1Cthe\x1b[1Curl\x1b[1Cbelow\x1b[39m\r\r\n\r\r\n")
	for len(url) > 0 {
		n := min(cols, len(url))
		sb.WriteString("\x1b[38;5;246m" + url[:n] + "\x1b[39m\r\r\n")
		url = url[n:]
	}
	sb.WriteString("\r\r\n\r\r\n\x1b[1CPaste\x1b[1Ccode\x1b[1Chere\x1b[1Cif\x1b[1Cprompted\x1b[1C>\r\r\n")
	return sb.String()
}

const testLoginURL = "https://claude.com/cai/oauth/authorize?code=true&client_id=00000000-0000-0000-0000-000000000000" +
	"&response_type=code&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback&scope=user%3Ainference" +
	"&code_challenge=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&code_challenge_method=S256&state=BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"

func TestClaudeLoginURLFromOutput(t *testing.T) {
	for _, cols := range []int{73, 40, 400} {
		out := []byte(claudeCodeScreen(testLoginURL, cols))
		w := &claudeLoginWatch{}
		for i := 0; i < len(out); i += 37 { // as the pty hands it over: in pieces
			w.feed(out[i:min(i+37, len(out))], cols)
		}
		if u, st := w.pending(); u != testLoginURL || st != "waiting" {
			t.Fatalf("cols %d: got %q (%s)", cols, u, st)
		}
		w.feed([]byte("\x1b[2K\x1b[1ALogin\x1b[1Csuccessful.\x1b[1CPress\x1b[1CEnter\r\r\n"), cols)
		if u, st := w.pending(); u != "" || st != "done" {
			t.Fatalf("after signing in: %q %s", u, st)
		}
	}
	// A link cut off mid-way (the screen still being written) isn't offered.
	w := &claudeLoginWatch{}
	w.feed([]byte(claudeCodeScreen(testLoginURL, 73))[:200], 73)
	if u, _ := w.pending(); u != "" {
		t.Fatalf("half a link: %q", u)
	}
	// A failed code is reported.
	w.feed([]byte(claudeCodeScreen(testLoginURL, 73)+"\x1b[1COAuth\x1b[1Cerror:\x1b[1CRequest\x1b[1Cfailed\r\r\n"), 73)
	if _, st := w.pending(); st != "error" {
		t.Fatalf("state %q after an OAuth error", st)
	}
}

func TestTermPlain(t *testing.T) {
	got := termPlain([]byte("a\x1b[3Cb\x1b[31mc\x1b[0m\r\nd\x1b]0;title\x07e\x1b]52;c;eA==\x1b\\f\x1b[1"))
	if got != "a   bc\nde"+"f" {
		t.Fatalf("%q", got)
	}
}

func TestVTClipboard(t *testing.T) {
	term := vt.New(20, 3)
	var got []string
	term.Clipboard = func(s string) { got = append(got, s) }
	enc := base64.StdEncoding.EncodeToString([]byte("https://example.com/é"))
	term.Write([]byte("x\x1b]52;c;" + enc + "\x07y\x1b]52;c;" + enc + "\x1b\\z\x1b]0;a title\x07\x1b]52;c;?\x07"))
	if len(got) != 2 || got[0] != "https://example.com/é" || got[1] != got[0] {
		t.Fatalf("copied %q", got)
	}
	if row, _ := term.Snapshot(0); string([]rune{row[0].Ch, row[1].Ch, row[2].Ch}) != "xyz" {
		t.Fatalf("the text around the sequences should still print")
	}
}

func TestClaudeConnectors(t *testing.T) {
	var cfg claudeConfig
	if _, err := cfg.addConnector("", "http://insecure.example.com/mcp", ""); err == nil {
		t.Fatal("http should be refused")
	}
	n1, err := cfg.addConnector("", "https://mcp.example.com/mcp", "tok")
	if err != nil || n1 != "mcp-example-com" {
		t.Fatalf("%q %v", n1, err)
	}
	n2, _ := cfg.addConnector("", "https://mcp.example.com/sse", "")
	if n2 != "mcp-example-com-2" {
		t.Fatalf("second name %q", n2)
	}
	if n, _ := cfg.addConnector("", "https://mcp.example.com/mcp", "tok2"); n != n1 || len(cfg.Connectors) != 2 || cfg.Connectors[0].Token != "tok2" {
		t.Fatalf("the same address again should update it: %+v", cfg.Connectors)
	}
	var mc struct {
		MCPServers map[string]struct {
			Type    string
			URL     string
			Headers map[string]string
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(mcpConfigJSON(cfg.Connectors)), &mc); err != nil {
		t.Fatal(err)
	}
	if s := mc.MCPServers[n1]; s.Type != "http" || s.Headers["Authorization"] != "Bearer tok2" {
		t.Fatalf("%+v", s)
	}
	if s := mc.MCPServers[n2]; s.Type != "sse" || s.Headers != nil {
		t.Fatalf("%+v", s)
	}
	if !cfg.removeConnector(n2) || cfg.removeConnector(n2) || len(cfg.Connectors) != 1 {
		t.Fatal("remove")
	}
	if mcpConfigJSON(nil) != "" {
		t.Fatal("no connectors: no config")
	}
}

func TestClaudeCodeProject(t *testing.T) {
	if p := claudeCodeProject("/root", "/root/margin-src"); p != "/root/.claude/projects/-root-margin-src" {
		t.Fatal(p)
	}
}

// A connector's call and result: shown, kept whole, and sent back unchanged.
func TestClaudeConnectorBlocks(t *testing.T) {
	ev := func(v string) string { return "data: " + v + "\n\n" }
	body := ev(`{"type":"message_start","message":{"usage":{"input_tokens":3}}}`) +
		ev(`{"type":"content_block_start","index":0,"content_block":{"type":"mcp_tool_use","id":"mcptoolu_1","name":"list_repos","server_name":"gh","input":{}}}`) +
		ev(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"owner\":\"me\"}"}}`) +
		ev(`{"type":"content_block_stop","index":0}`) +
		ev(`{"type":"content_block_start","index":1,"content_block":{"type":"mcp_tool_result","tool_use_id":"mcptoolu_1","is_error":false,"content":[{"type":"text","text":"margin\nsoma"}]}}`) +
		ev(`{"type":"content_block_stop","index":1}`) +
		ev(`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`) +
		ev(`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Two repos."}}`) +
		ev(`{"type":"content_block_stop","index":2}`) +
		ev(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}`) +
		ev(`{"type":"message_stop"}`)
	var done []cBlock
	rep, err := readStream(strings.NewReader(body), nil, nil, func(b cBlock) { done = append(done, b) })
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 2 || done[0].Type != "mcp_tool_use" || string(done[0].Input) != `{"owner":"me"}` || done[0].ServerName != "gh" {
		t.Fatalf("shown %+v", done)
	}
	if text, isErr := mcpResult(done[1].Raw); text != "margin\nsoma" || isErr {
		t.Fatalf("result %q %v", text, isErr)
	}
	b, _ := json.Marshal(cMsg{Role: "assistant", Content: rep.Blocks})
	for _, want := range []string{`"type":"mcp_tool_use"`, `"server_name":"gh"`, `"input":{"owner":"me"}`,
		`{"type":"mcp_tool_result","tool_use_id":"mcptoolu_1","is_error":false,"content":[{"type":"text","text":"margin\nsoma"}]}`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("sent back without %s:\n%s", want, b)
		}
	}
	var back cMsg // and read back from the saved conversation the same
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if b2, _ := json.Marshal(back); string(b2) != string(b) {
		t.Fatalf("round trip changed it:\n%s\n%s", b, b2)
	}
}

// With connectors, a request carries them and the MCP beta; without, neither.
func TestClaudeRequestConnectors(t *testing.T) {
	var gotBeta string
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBeta = r.Header.Get("anthropic-beta")
		b, _ := io.ReadAll(r.Body)
		got = nil
		json.Unmarshal(b, &got)
		io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()
	t.Setenv("MARGIN_CLAUDE_API", srv.URL)
	t.Setenv("NO_PROXY", "*")
	n := newClaudeNet()
	req := cRequest{Model: "m", MaxTokens: 10, Tools: append([]cTool(nil), claudeTools...)}
	if _, err := n.stream(context.Background(), "k", req, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if gotBeta != "" || got["mcp_servers"] != nil {
		t.Fatalf("no connectors: beta %q servers %v", gotBeta, got["mcp_servers"])
	}
	req.withConnectors([]claudeConnector{{Name: "gh", URL: "https://mcp.example.com/mcp", Token: "t"}})
	if _, err := n.stream(context.Background(), "k", req, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if gotBeta != mcpBeta {
		t.Fatalf("beta %q", gotBeta)
	}
	servers, _ := got["mcp_servers"].([]any)
	tools, _ := got["tools"].([]any)
	if len(servers) != 1 || servers[0].(map[string]any)["authorization_token"] != "t" || servers[0].(map[string]any)["type"] != "url" {
		t.Fatalf("servers %v", servers)
	}
	last := tools[len(tools)-1].(map[string]any)
	if last["type"] != "mcp_toolset" || last["mcp_server_name"] != "gh" || len(last) != 2 {
		t.Fatalf("toolset %v", last)
	}
	if first := tools[0].(map[string]any); first["name"] != "bash" || first["type"] != nil {
		t.Fatalf("Margin's own tools unchanged: %v", first)
	}
}
