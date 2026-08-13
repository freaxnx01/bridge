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
	Topics        []string  `json:"topics"`
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

// UpdateRepo patches description/private/archived. A nil pointer leaves that
// field unchanged; topics is a separate call (SetTopics) since it lives on
// its own endpoint.
func (c *ForgejoClient) UpdateRepo(ctx context.Context, owner, repo string, description *string, private, archived *bool) (RepoRef, error) {
	body := map[string]any{}
	if description != nil {
		body["description"] = *description
	}
	if private != nil {
		body["private"] = *private
	}
	if archived != nil {
		body["archived"] = *archived
	}
	var r fjRepo
	path := fmt.Sprintf("/api/v1/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	if err := c.patch(ctx, path, body, &r); err != nil {
		return RepoRef{}, err
	}
	vis := "public"
	if r.Private {
		vis = "private"
	}
	return RepoRef{
		Forge: "forgejo", Owner: r.Owner.Login, Name: r.Name,
		DefaultBranch: r.DefaultBranch, Description: r.Description, Topics: r.Topics,
		Visibility: vis, Archived: r.Archived,
		HTMLURL: r.HTMLURL, SSHURL: r.SSHURL, UpdatedAt: r.UpdatedAt,
	}, nil
}

// SetTopics replaces owner/repo's topic set via the dedicated topics
// endpoint. Gitea/Forgejo answers with 204 No Content, so there is nothing to
// decode back — the requested set is echoed on success.
func (c *ForgejoClient) SetTopics(ctx context.Context, owner, repo string, topics []string) ([]string, error) {
	if topics == nil {
		topics = []string{}
	}
	buf, err := json.Marshal(map[string]any{"topics": topics})
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/v1/repos/%s/%s/topics", url.PathEscape(owner), url.PathEscape(repo))
	req, err := http.NewRequestWithContext(ctx, "PUT", c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("forgejo %s: %s: %s", path, resp.Status, string(b))
	}
	return topics, nil
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

// PutFile creates or updates a file via the Forgejo/Gitea Contents API. Empty
// sha creates; a blob sha updates. Returns the file's html_url. Mirrors
// GithubClient.PutFile — Gitea's Contents API has the same PUT-with-optional-
// sha shape.
func (c *ForgejoClient) PutFile(ctx context.Context, owner, repo, path string, content []byte, message, sha string) (string, error) {
	body := map[string]any{
		"message": message,
		"content": base64.StdEncoding.EncodeToString(content),
	}
	if sha != "" {
		body["sha"] = sha
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("/api/v1/repos/%s/%s/contents/%s", url.PathEscape(owner), url.PathEscape(repo), escapePathSegments(path))
	req, err := http.NewRequestWithContext(ctx, "PUT", c.baseURL+endpoint, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("forgejo put %s: %s: %s", path, resp.Status, string(b))
	}
	var out struct {
		Content struct {
			HTMLURL string `json:"html_url"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Content.HTMLURL, nil
}

// ListTree lists path's entries from the repo's default branch. Non-recursive
// uses the Contents API (one level); recursive uses the Git Trees API with
// recursive=true. found is implicit: an empty repo returns an empty,
// non-truncated list with a nil error rather than an error.
func (c *ForgejoClient) ListTree(ctx context.Context, owner, repo, path string, recursive bool) ([]TreeEntry, bool, error) {
	if recursive {
		return c.listTreeRecursive(ctx, owner, repo, path)
	}
	return c.listTreeShallow(ctx, owner, repo, path)
}

func (c *ForgejoClient) listTreeShallow(ctx context.Context, owner, repo, path string) ([]TreeEntry, bool, error) {
	endpoint := fmt.Sprintf("/api/v1/repos/%s/%s/contents", url.PathEscape(owner), url.PathEscape(repo))
	if path != "" {
		endpoint += "/" + escapePathSegments(path)
	}
	resp, err := c.doGet(ctx, endpoint)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, err
	}
	if resp.StatusCode == http.StatusNotFound && isEmptyRepoMessage(body) {
		return nil, false, nil
	}
	if resp.StatusCode >= 400 {
		return nil, false, fmt.Errorf("forgejo list tree %s: %s: %s", path, resp.Status, string(body))
	}
	var raw []struct {
		Path string `json:"path"`
		Type string `json:"type"`
		Size int64  `json:"size"`
		SHA  string `json:"sha"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, fmt.Errorf("decode tree %s: %w", path, err)
	}
	entries := make([]TreeEntry, 0, len(raw))
	for _, e := range raw {
		entries = append(entries, TreeEntry{Path: e.Path, Type: e.Type, Size: e.Size, SHA: e.SHA})
	}
	return entries, false, nil
}

// defaultBranch fetches the repo's configured default branch. Used by
// listTreeRecursive to resolve the tree to fetch — an empty repo still has a
// default_branch value even before its first commit.
func (c *ForgejoClient) defaultBranch(ctx context.Context, owner, repo string) (string, error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.get(ctx, "/api/v1/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo), &r); err != nil {
		return "", err
	}
	return r.DefaultBranch, nil
}

func (c *ForgejoClient) listTreeRecursive(ctx context.Context, owner, repo, path string) ([]TreeEntry, bool, error) {
	branch, err := c.defaultBranch(ctx, owner, repo)
	if err != nil {
		return nil, false, err
	}
	endpoint := fmt.Sprintf("/api/v1/repos/%s/%s/git/trees/%s?recursive=true", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(branch))
	resp, err := c.doGet(ctx, endpoint)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, err
	}
	if (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusConflict) && isEmptyRepoMessage(body) {
		return nil, false, nil
	}
	if resp.StatusCode >= 400 {
		return nil, false, fmt.Errorf("forgejo list tree (recursive) %s: %s: %s", path, resp.Status, string(body))
	}
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(body, &tree); err != nil {
		return nil, false, fmt.Errorf("decode tree (recursive) %s: %w", path, err)
	}
	prefix := ""
	trimmed := strings.TrimSuffix(path, "/")
	if trimmed != "" {
		prefix = trimmed + "/"
	}
	entries := make([]TreeEntry, 0, len(tree.Tree))
	for _, e := range tree.Tree {
		if trimmed != "" && e.Path != trimmed && !strings.HasPrefix(e.Path, prefix) {
			continue
		}
		entries = append(entries, TreeEntry{Path: e.Path, Type: treeEntryType(e.Type, e.Mode), Size: e.Size, SHA: e.SHA})
	}
	return entries, tree.Truncated, nil
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

// GetIssue fetches a single issue's body plus its full comment thread, in
// chronological order. Comment-count truncation is the MCP handler's job,
// not the client's — this returns everything the forge has.
func (c *ForgejoClient) GetIssue(ctx context.Context, owner, repo string, number int) (Issue, []Comment, error) {
	var raw struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
		Body    string `json:"body"`
		Labels  []struct {
			Name string `json:"name"`
		} `json:"labels"`
		UpdatedAt time.Time `json:"updated_at"`
		CreatedAt time.Time `json:"created_at"`
	}
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d", owner, repo, number)
	if err := c.get(ctx, path, &raw); err != nil {
		return Issue{}, nil, err
	}
	labels := make([]string, 0, len(raw.Labels))
	for _, l := range raw.Labels {
		labels = append(labels, l.Name)
	}
	issue := Issue{
		Forge: "forgejo", Repo: owner + "/" + repo,
		Number: raw.Number, Title: raw.Title, URL: raw.HTMLURL,
		State: raw.State, Body: raw.Body, Labels: labels,
		Updated: raw.UpdatedAt, Created: raw.CreatedAt,
	}

	var rawComments []struct {
		ID     int    `json:"id"`
		Body   string `json:"body"`
		Poster struct {
			Login string `json:"login"`
		} `json:"poster"`
		CreatedAt time.Time `json:"created_at"`
	}
	commentsPath := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/comments", owner, repo, number)
	if err := c.get(ctx, commentsPath, &rawComments); err != nil {
		return Issue{}, nil, err
	}
	comments := make([]Comment, 0, len(rawComments))
	for _, rc := range rawComments {
		comments = append(comments, Comment{
			ID: rc.ID, Author: rc.Poster.Login, Body: rc.Body, Created: rc.CreatedAt,
		})
	}
	return issue, comments, nil
}
