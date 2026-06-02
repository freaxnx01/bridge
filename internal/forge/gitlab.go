package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type GitlabClient struct {
	token   string
	baseURL string
	http    *http.Client
}

func NewGitlabClient(token, baseURL string) *GitlabClient {
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}
	return &GitlabClient{token: token, baseURL: baseURL, http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *GitlabClient) Name() string { return "gitlab" }

func (c *GitlabClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("PRIVATE-TOKEN", c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gitlab %s: %s: %s", path, resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type glRepo struct {
	Name           string    `json:"name"`
	DefaultBranch  string    `json:"default_branch"`
	Description    string    `json:"description"`
	Visibility     string    `json:"visibility"`
	WebURL         string    `json:"web_url"`
	SSHURLToRepo   string    `json:"ssh_url_to_repo"`
	LastActivityAt time.Time `json:"last_activity_at"`
}

func (c *GitlabClient) ListRepos(ctx context.Context, owner string) ([]RepoRef, error) {
	var raw []glRepo
	if err := c.get(ctx, "/api/v4/users/"+owner+"/projects?per_page=100", &raw); err != nil {
		return nil, err
	}
	out := make([]RepoRef, 0, len(raw))
	for _, r := range raw {
		out = append(out, RepoRef{
			Forge: "gitlab", Owner: owner, Name: r.Name,
			DefaultBranch: r.DefaultBranch, Description: r.Description,
			Visibility: r.Visibility, HTMLURL: r.WebURL, SSHURL: r.SSHURLToRepo,
			UpdatedAt: r.LastActivityAt,
		})
	}
	return out, nil
}

type glIssue struct {
	IID       int       `json:"iid"`
	Title     string    `json:"title"`
	WebURL    string    `json:"web_url"`
	Labels    []string  `json:"labels"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (c *GitlabClient) ListOpenIssues(ctx context.Context, owner, repo string) ([]Issue, error) {
	proj := url.PathEscape(owner + "/" + repo)
	var raw []glIssue
	if err := c.get(ctx, "/api/v4/projects/"+proj+"/issues?state=opened&per_page=100", &raw); err != nil {
		return nil, err
	}
	out := make([]Issue, 0, len(raw))
	for _, i := range raw {
		out = append(out, Issue{
			Forge:   "gitlab",
			Repo:    owner + "/" + repo,
			Number:  i.IID,
			Title:   i.Title,
			URL:     i.WebURL,
			Labels:  i.Labels,
			Updated: i.UpdatedAt,
		})
	}
	return out, nil
}
