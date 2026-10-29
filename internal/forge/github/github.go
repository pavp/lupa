// Package github reads pull requests through the gh CLI. It never writes to the forge.
package github

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
)

// Ref names a pull request: a bare number refers to the repo of the working directory; a URL
// also names its host, owner and repo.
type Ref struct {
	Host, Owner, Repo string
	Number            int
}

// ParseRef reads `482`, `#482` or a pull request URL; anything else is not a PR reference.
func ParseRef(s string) (Ref, bool) {
	if n, err := strconv.Atoi(strings.TrimPrefix(s, "#")); err == nil {
		if n <= 0 {
			return Ref{}, false
		}
		return Ref{Number: n}, true
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return Ref{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return Ref{}, false
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 {
		return Ref{}, false
	}
	return Ref{Host: u.Host, Owner: parts[0], Repo: parts[1], Number: n}, true
}

// URL is the pull request's web address; empty for a bare number.
func (r Ref) URL() string {
	if r.Owner == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/%s/%s/pull/%d", r.Host, r.Owner, r.Repo, r.Number)
}

// SameRepo reports whether a git remote URL (https, ssh or scp-like) points at ref's repo.
func SameRepo(remote string, ref Ref) bool {
	host, path, ok := "", "", false
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		host, path, ok = u.Hostname(), u.Path, true
	} else if at, rest, found := strings.Cut(remote, "@"); found && !strings.Contains(at, "/") {
		host, path, ok = strings.Cut(rest, ":")
	}
	if !ok {
		return false
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(path, ".git"), "/"), "/")
	return len(parts) == 2 && strings.EqualFold(host, ref.Host) &&
		strings.EqualFold(parts[0], ref.Owner) && strings.EqualFold(parts[1], ref.Repo)
}

// PR is what Lupa reads of a pull request.
type PR struct {
	Ref          Ref
	Title, Body  string
	BaseRef      string
	HeadSHA      string
	LinkedIssues int
	Files        []string
}

// Client runs gh.
type Client struct {
	// Bin is the executable to run; empty means "gh" on PATH.
	Bin string
}

const prFields = "number,url,title,body,baseRefName,headRefOid,closingIssuesReferences,files"

// PR reads ref, running gh in dir so a bare number resolves against that clone's repo.
func (c Client) PR(dir string, ref Ref) (PR, error) {
	arg := ref.URL()
	if arg == "" {
		arg = strconv.Itoa(ref.Number)
	}
	out, err := c.run(dir, "pr", "view", arg, "--json", prFields)
	if err != nil {
		return PR{}, err
	}
	var raw struct {
		URL         string `json:"url"`
		Title       string `json:"title"`
		Body        string `json:"body"`
		BaseRefName string `json:"baseRefName"`
		HeadRefOid  string `json:"headRefOid"`
		Closing     []any  `json:"closingIssuesReferences"`
		Files       []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return PR{}, fmt.Errorf("gh pr view %s: unreadable answer: %w", arg, err)
	}
	got, ok := ParseRef(raw.URL)
	if !ok || raw.HeadRefOid == "" || raw.BaseRefName == "" {
		return PR{}, fmt.Errorf("gh pr view %s: answer lacks the url, base or head", arg)
	}
	pr := PR{Ref: got, Title: raw.Title, Body: raw.Body, BaseRef: raw.BaseRefName, HeadSHA: raw.HeadRefOid, LinkedIssues: len(raw.Closing)}
	for _, f := range raw.Files {
		pr.Files = append(pr.Files, f.Path)
	}
	return pr, nil
}

func (c Client) run(dir string, args ...string) ([]byte, error) {
	bin := c.Bin
	if bin == "" {
		bin = "gh"
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("gh %s: %s", strings.Join(args[:2], " "), strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("gh: %w", err)
	}
	return stdout.Bytes(), nil
}
