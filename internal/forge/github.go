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

type GithubClient struct {
	token   string
	baseURL string
	http    *http.Client
}

func NewGithubClient(token, baseURL string) *GithubClient {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &GithubClient{
		token:   token,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *GithubClient) Name() string { return "github" }

func (c *GithubClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github %s: %s: %s", path, resp.Status, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type ghRepo struct {
	Name          string    `json:"name"`
	DefaultBranch string    `json:"default_branch"`
	Description   string    `json:"description"`
	Topics        []string  `json:"topics"`
	Archived      bool      `json:"archived"`
	Visibility    string    `json:"visibility"`
	HTMLURL       string    `json:"html_url"`
	SSHURL        string    `json:"ssh_url"`
	UpdatedAt     time.Time `json:"updated_at"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

func (c *GithubClient) post(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode == http.StatusUnprocessableEntity {
		return ErrRepoExists
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github %s: %s: %s", path, resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *GithubClient) patch(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "PATCH", c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github %s: %s: %s", path, resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// CreateRepo creates a repo under the authenticated user (auto-initialized).
func (c *GithubClient) CreateRepo(ctx context.Context, name string, private bool) (RepoRef, error) {
	body := map[string]any{"name": name, "private": private, "auto_init": true}
	var r ghRepo
	if err := c.post(ctx, "/user/repos", body, &r); err != nil {
		return RepoRef{}, err
	}
	vis := r.Visibility
	if vis == "" {
		vis = "public"
	}
	return RepoRef{
		Forge: "github", Owner: r.Owner.Login, Name: r.Name,
		DefaultBranch: r.DefaultBranch, Visibility: vis,
		HTMLURL: r.HTMLURL, SSHURL: r.SSHURL, UpdatedAt: r.UpdatedAt,
	}, nil
}

// CreateIssue creates an issue on owner/repo and returns the minimal Issue.
func (c *GithubClient) CreateIssue(ctx context.Context, owner, repo, title, body string) (Issue, error) {
	req := map[string]any{"title": title, "body": body}
	var raw struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := c.post(ctx, "/repos/"+owner+"/"+repo+"/issues", req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge:   "github",
		Repo:    owner + "/" + repo,
		Number:  raw.Number,
		Title:   raw.Title,
		URL:     raw.HTMLURL,
		Updated: raw.UpdatedAt,
	}, nil
}

// CloseIssue closes owner/repo#number. stateReason, when non-empty, is one of
// GitHub's completed/not_planned/duplicate values; empty omits the field.
func (c *GithubClient) CloseIssue(ctx context.Context, owner, repo string, number int, stateReason string) (Issue, error) {
	req := map[string]any{"state": "closed"}
	if stateReason != "" {
		req["state_reason"] = stateReason
	}
	var raw struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		State     string    `json:"state"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number)
	if err := c.patch(ctx, path, req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge: "github", Repo: owner + "/" + repo,
		Number: raw.Number, Title: raw.Title, State: raw.State,
		URL: raw.HTMLURL, Updated: raw.UpdatedAt,
	}, nil
}

// UpdateIssue updates owner/repo#number's title and/or body. A nil pointer
// leaves that field unchanged; at least one is expected to be non-nil by the
// caller (the MCP handler enforces this at the boundary).
func (c *GithubClient) UpdateIssue(ctx context.Context, owner, repo string, number int, title, body *string) (Issue, error) {
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
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number)
	if err := c.patch(ctx, path, req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge: "github", Repo: owner + "/" + repo,
		Number: raw.Number, Title: raw.Title, State: raw.State,
		URL: raw.HTMLURL, Updated: raw.UpdatedAt,
	}, nil
}

// AddLabels adds labels to owner/repo#number and returns the issue's full
// label set after the call.
func (c *GithubClient) AddLabels(ctx context.Context, owner, repo string, number int, labels []string) ([]string, error) {
	req := map[string]any{"labels": labels}
	var raw []struct {
		Name string `json:"name"`
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/labels", owner, repo, number)
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
func (c *GithubClient) CommentIssue(ctx context.Context, owner, repo string, number int, body string) (Comment, error) {
	req := map[string]any{"body": body}
	var raw struct {
		ID        int       `json:"id"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
	if err := c.post(ctx, path, req, &raw); err != nil {
		return Comment{}, err
	}
	return Comment{ID: raw.ID, Body: raw.Body, Created: raw.CreatedAt}, nil
}

// ListRepos returns the repos owned by the authenticated user (the token's
// own account), including private ones. It uses the authenticated-user
// endpoint /user/repos rather than /users/{owner}/repos because the latter
// only ever returns public repos, even with a valid token — so private repos
// like obsidian-it would be invisible. Each forge owner is fetched with its
// own token (direnv-scoped), so affiliation=owner yields exactly that owner's
// public + private repos. The passed owner is a fallback label only.
func (c *GithubClient) ListRepos(ctx context.Context, owner string) ([]RepoRef, error) {
	var raw []ghRepo
	if err := c.get(ctx, "/user/repos?per_page=100&visibility=all&affiliation=owner", &raw); err != nil {
		return nil, err
	}
	out := make([]RepoRef, 0, len(raw))
	for _, r := range raw {
		if r.Archived {
			continue
		}
		o := r.Owner.Login
		if o == "" {
			o = owner
		}
		out = append(out, RepoRef{
			Forge:         "github",
			Owner:         o,
			Name:          r.Name,
			DefaultBranch: r.DefaultBranch,
			Description:   r.Description,
			Topics:        r.Topics,
			Visibility:    r.Visibility,
			HTMLURL:       r.HTMLURL,
			SSHURL:        r.SSHURL,
			UpdatedAt:     r.UpdatedAt,
		})
	}
	return out, nil
}

type ghIssue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	Labels  []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Milestone *struct {
		Title  string    `json:"title"`
		Number int       `json:"number"`
		DueOn  time.Time `json:"due_on"`
	} `json:"milestone"`
	UpdatedAt   time.Time `json:"updated_at"`
	CreatedAt   time.Time `json:"created_at"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
}

// ProjectItem is one GitHub Projects v2 board item, flattened for the roadmap.
type ProjectItem struct {
	Type   string // "Issue" | "DraftIssue" | "PullRequest"
	Repo   string // owner/name; "" for DraftIssue
	Title  string
	URL    string // "" for DraftIssue
	Status string // the board's Status single-select value; "" if unset
}

// graphqlPost issues a GraphQL query against <baseURL>/graphql and unmarshals
// the "data" object into out. A non-empty "errors" array is returned as an
// error (so INSUFFICIENT_SCOPES and similar surface clearly).
func (c *GithubClient) graphqlPost(ctx context.Context, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/graphql", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github graphql: %s: %s", resp.Status, string(body))
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return err
	}
	if len(env.Errors) > 0 {
		return fmt.Errorf("github graphql: %s", env.Errors[0].Message)
	}
	if out != nil && len(env.Data) > 0 {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

const projectV2ItemsQuery = `query($owner:String!, $number:Int!, $cursor:String){
  user(login:$owner){
    projectV2(number:$number){
      items(first:100, after:$cursor){
        pageInfo{ hasNextPage endCursor }
        nodes{
          content{
            __typename
            ... on Issue{ title url repository{ nameWithOwner } }
            ... on PullRequest{ title url repository{ nameWithOwner } }
            ... on DraftIssue{ title }
          }
          fieldValues(first:20){
            nodes{
              __typename
              ... on ProjectV2ItemFieldSingleSelectValue{ name field{ ... on ProjectV2FieldCommon{ name } } }
            }
          }
        }
      }
    }
  }
}`

// ListProjectV2Items returns every item on the user-level Projects v2 board
// (owner, number), flattened to ProjectItem with its Status single-select
// value. It paginates 100 at a time.
func (c *GithubClient) ListProjectV2Items(ctx context.Context, owner string, number int) ([]ProjectItem, error) {
	var out []ProjectItem
	cursor := ""
	for {
		vars := map[string]any{"owner": owner, "number": number, "cursor": nil}
		if cursor != "" {
			vars["cursor"] = cursor
		}
		var data struct {
			User struct {
				ProjectV2 struct {
					Items struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []struct {
							Content struct {
								Typename   string `json:"__typename"`
								Title      string `json:"title"`
								URL        string `json:"url"`
								Repository struct {
									NameWithOwner string `json:"nameWithOwner"`
								} `json:"repository"`
							} `json:"content"`
							FieldValues struct {
								Nodes []struct {
									Typename string `json:"__typename"`
									Name     string `json:"name"`
									Field    struct {
										Name string `json:"name"`
									} `json:"field"`
								} `json:"nodes"`
							} `json:"fieldValues"`
						} `json:"nodes"`
					} `json:"items"`
				} `json:"projectV2"`
			} `json:"user"`
		}
		if err := c.graphqlPost(ctx, projectV2ItemsQuery, vars, &data); err != nil {
			return nil, fmt.Errorf("list project v2 items %s/%d: %w", owner, number, err)
		}
		for _, n := range data.User.ProjectV2.Items.Nodes {
			item := ProjectItem{
				Type:  n.Content.Typename,
				Title: n.Content.Title,
				URL:   n.Content.URL,
				Repo:  n.Content.Repository.NameWithOwner,
			}
			for _, fv := range n.FieldValues.Nodes {
				if fv.Typename == "ProjectV2ItemFieldSingleSelectValue" && fv.Field.Name == "Status" {
					item.Status = fv.Name
					break
				}
			}
			out = append(out, item)
		}
		if !data.User.ProjectV2.Items.PageInfo.HasNextPage {
			break
		}
		cursor = data.User.ProjectV2.Items.PageInfo.EndCursor
	}
	return out, nil
}

func (c *GithubClient) ListOpenIssues(ctx context.Context, owner, repo string) ([]Issue, error) {
	var raw []ghIssue
	if err := c.get(ctx, "/repos/"+owner+"/"+repo+"/issues?state=open&per_page=100", &raw); err != nil {
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
		milestone := ""
		if i.Milestone != nil {
			milestone = i.Milestone.Title
		}
		out = append(out, Issue{
			Forge:     "github",
			Repo:      owner + "/" + repo,
			Number:    i.Number,
			Title:     i.Title,
			URL:       i.HTMLURL,
			Labels:    labels,
			Milestone: milestone,
			Updated:   i.UpdatedAt,
			Created:   i.CreatedAt,
		})
	}
	return out, nil
}

// GetFile fetches a file's decoded content and blob sha via the Contents API.
// found is false (with nil error) when the file does not exist (404).
func (c *GithubClient) GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.baseURL, url.PathEscape(owner), url.PathEscape(repo), escapePathSegments(path))
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, "", false, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", false, nil
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", false, fmt.Errorf("github get %s: %s: %s", path, resp.Status, string(body))
	}
	var gc struct {
		Content string `json:"content"`
		SHA     string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gc); err != nil {
		return nil, "", false, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(gc.Content, "\n", ""))
	if err != nil {
		return nil, "", false, fmt.Errorf("decode %s: %w", path, err)
	}
	return raw, gc.SHA, true, nil
}

// ListTree lists path's entries from the repo's default branch. Non-recursive
// uses the Contents API (one level); recursive uses the Git Trees API with
// recursive=1 and reports GitHub's truncated flag rather than silently
// dropping entries past its size limit. found is implicit: an empty repo
// returns an empty, non-truncated list with a nil error rather than an error.
func (c *GithubClient) ListTree(ctx context.Context, owner, repo, path string, recursive bool) ([]TreeEntry, bool, error) {
	if recursive {
		return c.listTreeRecursive(ctx, owner, repo, path)
	}
	return c.listTreeShallow(ctx, owner, repo, path)
}

func (c *GithubClient) listTreeShallow(ctx context.Context, owner, repo, path string) ([]TreeEntry, bool, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents", c.baseURL, url.PathEscape(owner), url.PathEscape(repo))
	if path != "" {
		endpoint += "/" + escapePathSegments(path)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, false, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
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
		return nil, false, fmt.Errorf("github list tree %s: %s: %s", path, resp.Status, string(body))
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
func (c *GithubClient) defaultBranch(ctx context.Context, owner, repo string) (string, error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.get(ctx, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo), &r); err != nil {
		return "", err
	}
	return r.DefaultBranch, nil
}

func (c *GithubClient) listTreeRecursive(ctx context.Context, owner, repo, path string) ([]TreeEntry, bool, error) {
	branch, err := c.defaultBranch(ctx, owner, repo)
	if err != nil {
		return nil, false, err
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1", c.baseURL, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(branch))
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, false, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
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
		return nil, false, fmt.Errorf("github list tree (recursive) %s: %s: %s", path, resp.Status, string(body))
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

// PutFile creates or updates a file via the Contents API. Empty sha creates;
// a blob sha updates. Returns the file's html_url.
func (c *GithubClient) PutFile(ctx context.Context, owner, repo, path string, content []byte, message, sha string) (string, error) {
	body := map[string]any{
		"message": message,
		"content": base64.StdEncoding.EncodeToString(content),
	}
	if sha != "" {
		body["sha"] = sha
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.baseURL, owner, repo, path)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("github put %s: %s: %s", path, resp.Status, string(b))
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

func (c *GithubClient) ListOpenMilestones(ctx context.Context, owner, repo string) ([]Milestone, error) {
	var raw []struct {
		Number int        `json:"number"`
		Title  string     `json:"title"`
		DueOn  *time.Time `json:"due_on"`
	}
	if err := c.get(ctx, "/repos/"+owner+"/"+repo+"/milestones?state=open&per_page=100", &raw); err != nil {
		return nil, err
	}
	out := make([]Milestone, 0, len(raw))
	for _, m := range raw {
		ms := Milestone{Number: m.Number, Title: m.Title}
		if m.DueOn != nil {
			ms.DueOn = *m.DueOn
		}
		out = append(out, ms)
	}
	return out, nil
}

func (c *GithubClient) ListOpenPullRequests(ctx context.Context, owner, repo string) ([]PullRequest, error) {
	var raw []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Draft  bool   `json:"draft"`
	}
	if err := c.get(ctx, "/repos/"+owner+"/"+repo+"/pulls?state=open&per_page=100", &raw); err != nil {
		return nil, err
	}
	out := make([]PullRequest, 0, len(raw))
	for _, p := range raw {
		out = append(out, PullRequest{Number: p.Number, Title: p.Title, Body: p.Body, Draft: p.Draft})
	}
	return out, nil
}

// RemoveLabel deletes one label from an issue. A 404 (label not present) is
// not an error — removal is idempotent by design, because the dispatcher
// re-runs label cleanup on every retry tick.
func (c *GithubClient) RemoveLabel(ctx context.Context, owner, repo string, number int, label string) error {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d/labels/%s",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), number, url.PathEscape(label))
	req, err := http.NewRequestWithContext(ctx, "DELETE", endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("remove label: %s", resp.Status)
	}
	return nil
}
