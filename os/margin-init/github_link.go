package main

// The tablet's GitHub link: a token pasted once from the phone (Send to Books page, "Link
// GitHub"), used to send the output of Clipboard commands to a private repo, where an AI
// helping on the PC or the web can read it ("I ran it: check"). The Claude tab can publish
// with it too.
//
// Outputs never go to the Margin repo itself: it's public, and output can show the Wi-Fi
// network, addresses, logs. The results repo must be private (checked when linking).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const githubLinkName = "/github.json"

// defaultResultsRepo is where outputs go unless the phone page says otherwise.
const defaultResultsRepo = "aminoulogie/margin-results"

type githubLink struct {
	Token   string `json:"token"`
	Results string `json:"results_repo"`
}

// githubLinkPath: Margin's folder, also when run from inside Alpine's chroot (cbsend, claude)
// through init's root.
func githubLinkPath() string {
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		return "/proc/1/root" + marginHome + githubLinkName
	}
	return marginHome + githubLinkName
}

func loadGitHubLink() githubLink {
	var l githubLink
	b, _ := os.ReadFile(githubLinkPath())
	json.Unmarshal(b, &l)
	if l.Results == "" {
		l.Results = defaultResultsRepo
	}
	return l
}

func saveGitHubLink(l githubLink) error {
	b, _ := json.Marshal(l)
	os.MkdirAll(marginHome, 0o755)
	return writeFileAtomic(githubLinkPath(), b, 0o600)
}

var repoNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// checkResultsRepo makes sure the token can see the repo and that it's private.
func checkResultsRepo(ctx context.Context, token, repo string) error {
	if !repoNameRE.MatchString(repo) {
		return fmt.Errorf("the results repo should look like owner/name")
	}
	var info struct {
		Private     bool `json:"private"`
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	gh := &ghClient{ctx: ctx, token: token, base: githubAPI() + "/repos/" + repo}
	if err := gh.call("GET", "", nil, &info); err != nil {
		if strings.Contains(err.Error(), "404") {
			return fmt.Errorf("can't see %s: create it on github.com (private), and give the token access to it", repo)
		}
		return err
	}
	if !info.Private {
		return fmt.Errorf("%s is public: outputs can show your Wi-Fi and logs, so it must be private (Settings > Danger Zone > Change visibility)", repo)
	}
	return nil
}

// sendResult puts one command's output in the results repo as a new file, and says where.
func sendCmdResult(ctx context.Context, l githubLink, title, cmd, out string, code int, when time.Time) (string, error) {
	if l.Token == "" {
		return "", fmt.Errorf("the tablet isn't linked to GitHub yet: on your phone, open the Send to Books page (its address is in Library) and use Link GitHub")
	}
	if len(out) > 4<<20 {
		out = out[:1<<20] + "\n[… cut …]\n" + out[len(out)-3<<20:]
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\nRun on the tablet %s, exit code %d.\n\n## Command\n```\n%s\n```\n\n## Output\n```\n%s\n```\n",
		title, when.Format("2006-01-02 15:04:05"), code, strings.TrimRight(cmd, "\n"), strings.TrimRight(out, "\n"))
	path := "results/" + when.Format("2006-01-02") + "/" + when.Format("150405") + "-" + slugify(title) + ".md"
	body := map[string]string{
		"message": "Output: " + title,
		"content": base64.StdEncoding.EncodeToString([]byte(sb.String())),
	}
	gh := &ghClient{ctx: ctx, token: l.Token, base: githubAPI() + "/repos/" + l.Results}
	if err := gh.call("PUT", "/contents/"+path, body, nil); err != nil {
		return "", err
	}
	return l.Results + "/" + path, nil
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.Trim(slugRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "-")
	}
	if s == "" {
		s = "output"
	}
	return s
}

// runCBSend is "margin-init cbsend <base>": cbrun (in Alpine) calls it after a Clipboard
// command ran, with base.title, base.sh, base.out and base.code next to each other.
func runCBSend(base string) int {
	read := func(ext string) string { b, _ := os.ReadFile(base + ext); return string(b) }
	code := 0
	fmt.Sscan(strings.TrimSpace(read(".code")), &code)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	where, err := sendCmdResult(ctx, loadGitHubLink(), strings.TrimSpace(read(".title")), read(".sh"), read(".out"), code, time.Now())
	if err != nil {
		fmt.Printf("\033[31mThe output wasn't sent: %s\033[0m\n", err)
		return 1
	}
	fmt.Printf("\033[32m✓ Output sent to GitHub (%s).\033[0m Tell the AI it's done.\n", where)
	return 0
}

// githubLinkPost saves the token pasted on the phone (Send to Books page).
func (c *console) githubLinkPost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	reply := func(v map[string]any) { json.NewEncoder(w).Encode(v) }
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		reply(map[string]any{"error": "POST the token here"})
		return
	}
	if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
		w.WriteHeader(http.StatusForbidden)
		reply(map[string]any{"error": "only this page can link GitHub"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		reply(map[string]any{"error": "bad form"})
		return
	}
	l := githubLink{Token: strings.TrimSpace(r.PostForm.Get("token")), Results: strings.TrimSpace(r.PostForm.Get("repo"))}
	if l.Results == "" {
		l.Results = defaultResultsRepo
	}
	if l.Token == "" {
		reply(map[string]any{"error": "paste the token"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := checkResultsRepo(ctx, l.Token, l.Results); err != nil {
		reply(map[string]any{"error": err.Error()})
		return
	}
	if err := saveGitHubLink(l); err != nil {
		reply(map[string]any{"error": err.Error()})
		return
	}
	go func() {
		drawMu.Lock()
		defer drawMu.Unlock()
		c.toastMsg("GitHub linked")
	}()
	reply(map[string]any{"ok": true, "repo": l.Results})
}

// githubLinkStatus is the line the phone page shows.
func githubLinkStatus() string {
	l := loadGitHubLink()
	if l.Token == "" {
		return "Not linked yet."
	}
	return "Linked: outputs go to " + l.Results + ". Paste again to change it."
}
