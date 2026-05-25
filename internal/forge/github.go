package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	defer resp.Body.Close()
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
	Visibility    string    `json:"visibility"`
	HTMLURL       string    `json:"html_url"`
	SSHURL        string    `json:"ssh_url"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (c *GithubClient) ListRepos(ctx context.Context, owner string) ([]RepoRef, error) {
	var raw []ghRepo
	if err := c.get(ctx, "/users/"+owner+"/repos?per_page=100&type=owner", &raw); err != nil {
		return nil, err
	}
	out := make([]RepoRef, 0, len(raw))
	for _, r := range raw {
		out = append(out, RepoRef{
			Forge:         "github",
			Owner:         owner,
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
	Number      int                      `json:"number"`
	Title       string                   `json:"title"`
	HTMLURL     string                   `json:"html_url"`
	Labels      []struct{ Name string `json:"name"` } `json:"labels"`
	UpdatedAt   time.Time                `json:"updated_at"`
	PullRequest *struct{ URL string `json:"url"` } `json:"pull_request"`
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
		out = append(out, Issue{
			Forge:   "github",
			Repo:    owner + "/" + repo,
			Number:  i.Number,
			Title:   i.Title,
			URL:     i.HTMLURL,
			Labels:  labels,
			Updated: i.UpdatedAt,
		})
	}
	return out, nil
}
