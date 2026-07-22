package forge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ForgejoClient struct {
	token   string
	baseURL string
	http    *http.Client
}

func NewForgejoClient(token, baseURL string) *ForgejoClient {
	if baseURL == "" {
		baseURL = "https://codeberg.org"
	}
	return &ForgejoClient{token: token, baseURL: baseURL, http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *ForgejoClient) Name() string { return "forgejo" }

// doGet issues an authenticated GET request against path and returns the raw
// response. Callers are responsible for closing the body and interpreting
// the status code (get() and GetFile diverge only in what they do with it).
func (c *ForgejoClient) doGet(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	return c.http.Do(req)
}

func (c *ForgejoClient) get(ctx context.Context, path string, out any) error {
	resp, err := c.doGet(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("forgejo %s: %s: %s", path, resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type fjRepo struct {
	Name          string    `json:"name"`
	DefaultBranch string    `json:"default_branch"`
	Description   string    `json:"description"`
	Private       bool      `json:"private"`
	Archived      bool      `json:"archived"`
	HTMLURL       string    `json:"html_url"`
	SSHURL        string    `json:"ssh_url"`
	UpdatedAt     time.Time `json:"updated_at"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

func (c *ForgejoClient) post(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(buf))
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode == http.StatusConflict {
		return ErrRepoExists
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("forgejo %s: %s: %s", path, resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *ForgejoClient) patch(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "PATCH", c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("forgejo %s: %s: %s", path, resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// CreateRepo creates a repo under the authenticated user (auto-initialized).
func (c *ForgejoClient) CreateRepo(ctx context.Context, name string, private bool) (RepoRef, error) {
	body := map[string]any{
		"name": name, "private": private, "auto_init": true, "default_branch": "main",
	}
	var r fjRepo
	if err := c.post(ctx, "/api/v1/user/repos", body, &r); err != nil {
		return RepoRef{}, err
	}
	vis := "public"
	if r.Private {
		vis = "private"
	}
	return RepoRef{
		Forge: "forgejo", Owner: r.Owner.Login, Name: r.Name,
		DefaultBranch: r.DefaultBranch, Visibility: vis,
		HTMLURL: r.HTMLURL, SSHURL: r.SSHURL, UpdatedAt: r.UpdatedAt,
	}, nil
}

// CreateIssue creates an issue on owner/repo via Forgejo/Gitea and returns the
// minimal Issue.
func (c *ForgejoClient) CreateIssue(ctx context.Context, owner, repo, title, body string) (Issue, error) {
	req := map[string]any{"title": title, "body": body}
	var raw struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := c.post(ctx, "/api/v1/repos/"+owner+"/"+repo+"/issues", req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge:   "forgejo",
		Repo:    owner + "/" + repo,
		Number:  raw.Number,
		Title:   raw.Title,
		URL:     raw.HTMLURL,
		Updated: raw.UpdatedAt,
	}, nil
}

// CloseIssue closes owner/repo#number. stateReason is accepted for interface
// parity with GithubClient but Forgejo/Gitea has no equivalent field, so it
// is never sent.
func (c *ForgejoClient) CloseIssue(ctx context.Context, owner, repo string, number int, _ string) (Issue, error) {
	req := map[string]any{"state": "closed"}
	var raw struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		State     string    `json:"state"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d", owner, repo, number)
	if err := c.patch(ctx, path, req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge: "forgejo", Repo: owner + "/" + repo,
		Number: raw.Number, Title: raw.Title, State: raw.State,
		URL: raw.HTMLURL, Updated: raw.UpdatedAt,
	}, nil
}

// UpdateIssue updates owner/repo#number's title and/or body. A nil pointer
// leaves that field unchanged.
func (c *ForgejoClient) UpdateIssue(ctx context.Context, owner, repo string, number int, title, body *string) (Issue, error) {
	req := map[string]any{}
	if title != nil {
		req["title"] = *title
	}
	if body != nil {
		req["body"] = *body
	}
	var raw struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		State     string    `json:"state"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d", owner, repo, number)
	if err := c.patch(ctx, path, req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge: "forgejo", Repo: owner + "/" + repo,
		Number: raw.Number, Title: raw.Title, State: raw.State,
		URL: raw.HTMLURL, Updated: raw.UpdatedAt,
	}, nil
}

// AddLabels adds labels to owner/repo#number and returns the issue's full
// label set after the call.
func (c *ForgejoClient) AddLabels(ctx context.Context, owner, repo string, number int, labels []string) ([]string, error) {
	req := map[string]any{"labels": labels}
	var raw []struct {
		Name string `json:"name"`
	}
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/labels", owner, repo, number)
	if err := c.post(ctx, path, req, &raw); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		out = append(out, l.Name)
	}
	return out, nil
}

// CommentIssue posts a comment on owner/repo#number.
func (c *ForgejoClient) CommentIssue(ctx context.Context, owner, repo string, number int, body string) (Comment, error) {
	req := map[string]any{"body": body}
	var raw struct {
		ID        int       `json:"id"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
	}
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/comments", owner, repo, number)
	if err := c.post(ctx, path, req, &raw); err != nil {
		return Comment{}, err
	}
	return Comment{ID: raw.ID, Body: raw.Body, Created: raw.CreatedAt}, nil
}

func (c *ForgejoClient) ListRepos(ctx context.Context, owner string) ([]RepoRef, error) {
	var raw []fjRepo
	if err := c.get(ctx, "/api/v1/users/"+url.PathEscape(owner)+"/repos?limit=50", &raw); err != nil {
		return nil, err
	}
	out := make([]RepoRef, 0, len(raw))
	for _, r := range raw {
		if r.Archived {
			continue
		}
		vis := "public"
		if r.Private {
			vis = "private"
		}
		out = append(out, RepoRef{
			Forge: "forgejo", Owner: owner, Name: r.Name,
			DefaultBranch: r.DefaultBranch, Description: r.Description,
			Visibility: vis, HTMLURL: r.HTMLURL, SSHURL: r.SSHURL,
			UpdatedAt: r.UpdatedAt,
		})
	}
	return out, nil
}

// GetFile fetches a file's decoded content and blob sha via the Forgejo/Gitea
// Contents API. found is false (with nil error) when the file does not exist
// (404). Content is read from the repository's default branch.
func (c *ForgejoClient) GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error) {
	endpoint := fmt.Sprintf("/api/v1/repos/%s/%s/contents/%s", url.PathEscape(owner), url.PathEscape(repo), escapePathSegments(path))
	resp, err := c.doGet(ctx, endpoint)
	if err != nil {
		return nil, "", false, err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", false, nil
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, "", false, fmt.Errorf("forgejo get %s: %s: %s", path, resp.Status, string(b))
	}
	var fc struct {
		Content string `json:"content"`
		SHA     string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fc); err != nil {
		return nil, "", false, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(fc.Content, "\n", ""))
	if err != nil {
		return nil, "", false, fmt.Errorf("decode %s: %w", path, err)
	}
	return raw, fc.SHA, true, nil
}

type fjIssue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	Labels  []struct {
		Name string `json:"name"`
	} `json:"labels"`
	UpdatedAt   time.Time `json:"updated_at"`
	PullRequest any       `json:"pull_request"`
}

func (c *ForgejoClient) ListOpenIssues(ctx context.Context, owner, repo string) ([]Issue, error) {
	var raw []fjIssue
	if err := c.get(ctx, "/api/v1/repos/"+owner+"/"+repo+"/issues?state=open&type=issues&limit=50", &raw); err != nil {
		return nil, err
	}
	out := make([]Issue, 0, len(raw))
	for _, i := range raw {
		if i.PullRequest != nil {
			continue
		}
		labels := make([]string, 0, len(i.Labels))
		for _, l := range i.Labels {
			labels = append(labels, l.Name)
		}
		out = append(out, Issue{
			Forge: "forgejo", Repo: owner + "/" + repo,
			Number: i.Number, Title: i.Title, URL: i.HTMLURL,
			Labels: labels, Updated: i.UpdatedAt,
		})
	}
	return out, nil
}
