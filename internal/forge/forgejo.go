package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

func (c *ForgejoClient) get(ctx context.Context, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
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
	HTMLURL       string    `json:"html_url"`
	SSHURL        string    `json:"ssh_url"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (c *ForgejoClient) ListRepos(ctx context.Context, owner string) ([]RepoRef, error) {
	var raw []fjRepo
	if err := c.get(ctx, "/api/v1/users/"+owner+"/repos?limit=50", &raw); err != nil {
		return nil, err
	}
	out := make([]RepoRef, 0, len(raw))
	for _, r := range raw {
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

type fjIssue struct {
	Number      int                    `json:"number"`
	Title       string                 `json:"title"`
	HTMLURL     string                 `json:"html_url"`
	Labels      []struct{ Name string `json:"name"` } `json:"labels"`
	UpdatedAt   time.Time              `json:"updated_at"`
	PullRequest any                    `json:"pull_request"`
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
