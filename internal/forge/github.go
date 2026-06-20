package forge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
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
	UpdatedAt   time.Time `json:"updated_at"`
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
	defer resp.Body.Close()
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

// GetFile fetches a file's decoded content and blob sha via the Contents API.
// found is false (with nil error) when the file does not exist (404).
func (c *GithubClient) GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error) {
	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.baseURL, owner, repo, path)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
