package main

// publish_to_github: the fixes Claude made on the tablet go to GitHub in one commit, through
// GitHub's API (git itself can't run here: Alpine's git needs getrandom(), which kernel 3.4
// lacks). What changed is found against .margin-manifest, the md5 of every file `update`
// unpacked (update.sh writes it); after publishing, the manifest is brought up to date.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const manifestName = ".margin-manifest"

func githubAPI() string {
	if u := os.Getenv("MARGIN_GITHUB_API"); u != "" {
		return u
	}
	return "https://api.github.com"
}

// readManifest reads md5sum's output ("<md5>  ./path") into path → md5.
func readManifest(src string) (map[string]string, error) {
	f, err := os.Open(filepath.Join(src, manifestName))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		sum, path, ok := strings.Cut(sc.Text(), "  ")
		if !ok {
			continue
		}
		m[strings.TrimPrefix(path, "./")] = sum
	}
	return m, sc.Err()
}

// scanTree is path → md5 for every file under src (but the manifest and .git).
func scanTree(src string) (map[string]string, map[string]bool, error) {
	sums, exec := map[string]string{}, map[string]bool{}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || d.Name() == manifestName {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		rel = filepath.ToSlash(rel)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := md5.Sum(b)
		sums[rel] = hex.EncodeToString(h[:])
		if info, err := d.Info(); err == nil && info.Mode()&0o111 != 0 {
			exec[rel] = true
		}
		return nil
	})
	return sums, exec, err
}

// localChanges lists files changed or added since update unpacked src, and files deleted.
func localChanges(src string) (changed, deleted []string, exec map[string]bool, err error) {
	old, err := readManifest(src)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("no %s in %s: run `update` once so there's a known starting point", manifestName, src)
	}
	now, exec, err := scanTree(src)
	if err != nil {
		return nil, nil, nil, err
	}
	for p, s := range now {
		if old[p] != s {
			changed = append(changed, p)
		}
	}
	for p := range old {
		if _, ok := now[p]; !ok {
			deleted = append(deleted, p)
		}
	}
	sort.Strings(changed)
	sort.Strings(deleted)
	return changed, deleted, exec, nil
}

// writeManifest records src as it is now (after publishing, it matches GitHub).
func writeManifest(src string) error {
	now, _, err := scanTree(src)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(now))
	for p := range now {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var sb strings.Builder
	for _, p := range paths {
		fmt.Fprintf(&sb, "%s  ./%s\n", now[p], p)
	}
	return writeFileAtomic(filepath.Join(src, manifestName), []byte(sb.String()), 0o644)
}

// publishFixes commits the local changes on top of the branch's newest commit.
func publishFixes(ctx context.Context, token, repo, branch, src, message string) (string, error) {
	if token == "" {
		return "", fmt.Errorf("no GitHub token: the user can set one with /github")
	}
	changed, deleted, exec, err := localChanges(src)
	if err != nil {
		return "", err
	}
	if len(changed)+len(deleted) == 0 {
		return "nothing to publish: no files changed since the last update", nil
	}
	if strings.TrimSpace(message) == "" {
		message = "Fix from the tablet"
	}
	gh := &ghClient{ctx: ctx, token: token, base: githubAPI() + "/repos/" + repo}
	var ref struct {
		Object struct{ SHA string } `json:"object"`
	}
	if err := gh.call("GET", "/git/ref/heads/"+branch, nil, &ref); err != nil {
		return "", err
	}
	var head struct {
		Tree struct{ SHA string } `json:"tree"`
	}
	if err := gh.call("GET", "/git/commits/"+ref.Object.SHA, nil, &head); err != nil {
		return "", err
	}
	type entry struct {
		Path string  `json:"path"`
		Mode string  `json:"mode"`
		Type string  `json:"type"`
		SHA  *string `json:"sha"`
	}
	var tree []entry
	for _, p := range changed {
		b, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(p)))
		if err != nil {
			return "", err
		}
		var blob struct{ SHA string }
		if err := gh.call("POST", "/git/blobs", map[string]string{"content": base64.StdEncoding.EncodeToString(b), "encoding": "base64"}, &blob); err != nil {
			return "", fmt.Errorf("%s: %w", p, err)
		}
		mode := "100644"
		if exec[p] {
			mode = "100755"
		}
		sha := blob.SHA
		tree = append(tree, entry{p, mode, "blob", &sha})
	}
	for _, p := range deleted {
		tree = append(tree, entry{p, "100644", "blob", nil}) // a null sha removes it
	}
	var newTree, commit struct{ SHA string }
	if err := gh.call("POST", "/git/trees", map[string]any{"base_tree": head.Tree.SHA, "tree": tree}, &newTree); err != nil {
		return "", err
	}
	if err := gh.call("POST", "/git/commits", map[string]any{"message": message, "tree": newTree.SHA, "parents": []string{ref.Object.SHA}}, &commit); err != nil {
		return "", err
	}
	if err := gh.call("PATCH", "/git/refs/heads/"+branch, map[string]any{"sha": commit.SHA}, nil); err != nil {
		return "", err
	}
	writeManifest(src)
	files := append(append([]string(nil), changed...), deleted...)
	list := strings.Join(files[:min(len(files), 8)], ", ")
	if len(files) > 8 {
		list += fmt.Sprintf(" and %d more", len(files)-8)
	}
	return fmt.Sprintf("Published %s to %s@%s as %.7s: %s", plural(len(files), "file"), repo, branch, commit.SHA, list), nil
}

type ghClient struct {
	ctx         context.Context
	token, base string
}

func (g *ghClient) call(method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	resp, err := newClaudeNet().do(func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(g.ctx, method, g.base+path, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+g.token)
		r.Header.Set("Accept", "application/vnd.github+json")
		r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		return r, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		var e struct{ Message string }
		json.Unmarshal(b, &e)
		switch resp.StatusCode {
		case 401:
			return fmt.Errorf("GitHub refused the token (set a new one with /github)")
		case 403, 404:
			return fmt.Errorf("GitHub %d: %s (does the token have Contents: read and write on this repo?)", resp.StatusCode, e.Message)
		}
		return fmt.Errorf("GitHub %d: %s", resp.StatusCode, e.Message)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}
