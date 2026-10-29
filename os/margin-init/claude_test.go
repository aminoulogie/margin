package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeReadStream(t *testing.T) {
	ev := func(v string) string { return "event: x\ndata: " + v + "\n\n" }
	body := ev(`{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":5}}}`) +
		ev(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		ev(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`) +
		ev(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`) +
		ev(`{"type":"content_block_stop","index":0}`) +
		ev(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"bash","input":{}}}`) +
		ev(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}`) +
		ev(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}`) +
		ev(`{"type":"content_block_stop","index":1}`) +
		ev(`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`) +
		ev(`{"type":"message_stop"}`)
	var text, tools string
	rep, err := readStream(strings.NewReader(body), func(s string) { text += s }, func(n string) { tools += n })
	if err != nil {
		t.Fatal(err)
	}
	if text != "Hello" || tools != "bash" || rep.StopReason != "tool_use" || rep.InTokens != 15 || rep.OutTokens != 7 {
		t.Fatalf("text %q tools %q rep %+v", text, tools, rep)
	}
	if len(rep.Blocks) != 2 || string(rep.Blocks[1].Input) != `{"command":"ls"}` || rep.Blocks[1].ID != "t1" {
		t.Fatalf("blocks %+v", rep.Blocks)
	}
	// Cut short: an error, with what had arrived.
	rep, err = readStream(strings.NewReader(body[:200]), nil, nil)
	if err == nil || rep == nil {
		t.Fatal("a cut stream should be an error")
	}
}

func TestClaudeEditFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.go")
	os.WriteFile(p, []byte("one\ntwo\nthree\ntwo\n"), 0o644)
	if _, _, err := editFile(p, "two\n", "2\n", false); err == nil {
		t.Fatal("two matches without replace_all should fail")
	}
	if _, _, err := editFile(p, "four", "4", false); err == nil {
		t.Fatal("no match should fail")
	}
	msg, diff, err := editFile(p, "one\ntwo\n", "one\nTWO\nmore\n", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "one\nTWO\nmore\nthree\ntwo\n" {
		t.Fatalf("file %q", b)
	}
	if !strings.Contains(msg, "2 additions and 1 removal") || len(diff) != 3 || !strings.Contains(diff[0], "2 - two") {
		t.Fatalf("msg %q diff %q", msg, diff)
	}
	if _, _, err := editFile(p, "two", "2", true); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeReadNumbered(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	os.WriteFile(p, []byte("a\nb\nc\n"), 0o644)
	s, n, err := readNumbered(p, 2, 1)
	if err != nil || n != 1 || s != "     2\tb\n[1 more lines: read on with offset 3]\n" {
		t.Fatalf("%q %d %v", s, n, err)
	}
}

func TestClaudeEditLayout(t *testing.T) {
	lines, r, c := editLayout([]rune("abcdefghij\nxy"), 12, 10) // rows of 8 after "❯ "
	if strings.Join(lines, "|") != "abcdefgh|ij|xy" || r != 2 || c != 1 {
		t.Fatalf("%q %d %d", lines, r, c)
	}
	_, r, c = editLayout([]rune("abcdefgh"), 8, 10) // after a full row: still on it, in the margin column
	if r != 0 || c != 8 {
		t.Fatalf("full row: %d %d", r, c)
	}
}

func TestClaudeMarkdownWraps(t *testing.T) {
	out, code := mdLine("some **bold** words and `code` that go on long enough to wrap", false, 30, "● ")
	if code {
		t.Fatal("not code")
	}
	for _, row := range strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n") {
		if visibleLen(row) > 29 {
			t.Fatalf("row too wide (%d): %q", visibleLen(row), row)
		}
	}
	if !strings.Contains(out, cBold+"bold") || !strings.Contains(out, cCode+"code") {
		t.Fatalf("styles: %q", out)
	}
	if out, code = mdLine("```go", false, 30, "  "); out != "" || !code {
		t.Fatal("a fence starts code")
	}
}

func TestClaudeCapWriter(t *testing.T) {
	var w capWriter
	big := strings.Repeat("x", 100<<10)
	in := "start\n" + big + "\nend\r\nprogress 1\rprogress 2\n"
	if n, _ := w.Write([]byte(in)); n != len(in) {
		t.Fatalf("Write returned %d", n)
	}
	s := w.String()
	if !strings.HasPrefix(s, "start\n") || !strings.Contains(s, "bytes left out") || !strings.HasSuffix(s, "end\nprogress 2\n") {
		t.Fatalf("got %q…%q", s[:20], s[len(s)-40:])
	}
}

// publishFixes against a fake GitHub: the changed, added and deleted files go in one commit.
func TestClaudePublish(t *testing.T) {
	src := t.TempDir()
	write := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(src, p)), 0o755)
		os.WriteFile(filepath.Join(src, p), []byte(s), 0o644)
	}
	write("a.go", "a")
	write("os/b.go", "b")
	write("gone.txt", "x")
	if err := writeManifest(src); err != nil {
		t.Fatal(err)
	}
	write("a.go", "A")
	write("new.go", "n")
	os.Remove(filepath.Join(src, "gone.txt"))
	changed, deleted, _, err := localChanges(src)
	if err != nil || strings.Join(changed, ",") != "a.go,new.go" || strings.Join(deleted, ",") != "gone.txt" {
		t.Fatalf("changed %v deleted %v %v", changed, deleted, err)
	}
	var tree []map[string]any
	var patched string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/o/r/git/ref/heads/main":
			w.Write([]byte(`{"object":{"sha":"head1"}}`))
		case r.Method == "GET" && r.URL.Path == "/repos/o/r/git/commits/head1":
			w.Write([]byte(`{"tree":{"sha":"tree1"}}`))
		case r.URL.Path == "/repos/o/r/git/blobs":
			w.Write([]byte(`{"sha":"blob-` + body["content"].(string) + `"}`))
		case r.URL.Path == "/repos/o/r/git/trees":
			for _, e := range body["tree"].([]any) {
				tree = append(tree, e.(map[string]any))
			}
			w.Write([]byte(`{"sha":"tree2"}`))
		case r.URL.Path == "/repos/o/r/git/commits":
			if body["tree"] != "tree2" || body["parents"].([]any)[0] != "head1" {
				w.WriteHeader(422)
			}
			w.Write([]byte(`{"sha":"commit2abcdef"}`))
		case r.Method == "PATCH" && r.URL.Path == "/repos/o/r/git/refs/heads/main":
			patched = body["sha"].(string)
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	t.Setenv("MARGIN_GITHUB_API", srv.URL)
	t.Setenv("NO_PROXY", "*")
	oldClient := webClient
	webClient = func() *http.Client { return srv.Client() } // (margin-init's side goes through it)
	t.Cleanup(func() { webClient = oldClient })
	if _, err := publishFixes(context.Background(), "bad", "o/r", "main", src, "m"); err == nil {
		t.Fatal("a refused token should fail")
	}
	res, err := publishFixes(context.Background(), "tok", "o/r", "main", src, "Fix it")
	if err != nil {
		t.Fatal(err)
	}
	if patched != "commit2abcdef" || len(tree) != 3 || !strings.Contains(res, "3 files") {
		t.Fatalf("patched %q tree %v res %q", patched, tree, res)
	}
	for _, e := range tree {
		if e["path"] == "gone.txt" && e["sha"] != nil || e["path"] == "a.go" && e["sha"] != "blob-QQ==" {
			t.Fatalf("tree entry %v", e)
		}
	}
	if c, d, _, _ := localChanges(src); len(c)+len(d) != 0 {
		t.Fatalf("after publishing the manifest should match: %v %v", c, d)
	}
}
