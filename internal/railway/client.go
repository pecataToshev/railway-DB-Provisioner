// Package railway wraps the Railway GraphQL API for variable management
// and the Railway CLI (`railway up`) for deploys.
//
// Variables are managed via GraphQL (with unrendered: true) so that
// references like ${{Service.VAR}} are returned as-is, enabling
// staleness detection. Deploys use `railway up` which uploads local
// files to Railway for building — works without a connected GitHub repo.
package railway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
)

const graphqlEndpoint = "https://backboard.railway.com/graphql/v2"

// Client wraps the Railway GraphQL API and CLI, authenticated via RAILWAY_TOKEN.
type Client struct {
	token         string
	httpClient    *http.Client
	projectID     string
	environmentID string
}

// NewClient returns a Client that authenticates to Railway with the given token.
// Call ResolveIDs to populate projectID and environmentID from the token.
func NewClient(token string) *Client {
	return &Client{
		token:      token,
		httpClient: &http.Client{},
	}
}

// graphqlRequest sends a GraphQL query/mutation and returns the data.
func (c *Client) graphqlRequest(query string, variables map[string]any) (map[string]any, error) {
	body := map[string]any{
		"query":     query,
		"variables": variables,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", graphqlEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Project-Access-Token", c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("railway API returned %d: %s", resp.StatusCode, string(raw))
	}

	var result struct {
		Data   map[string]any `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("railway API error: %s", result.Errors[0].Message)
	}
	return result.Data, nil
}

// ResolveIDs queries the token to get projectId and environmentId.
// This must be called before any variable operations.
func (c *Client) ResolveIDs() error {
	data, err := c.graphqlRequest(
		`query { projectToken { projectId environmentId } }`,
		nil,
	)
	if err != nil {
		return fmt.Errorf("resolve token: %w", err)
	}

	pt, ok := data["projectToken"].(map[string]any)
	if !ok {
		return fmt.Errorf("projectToken not found in response")
	}

	c.projectID, _ = pt["projectId"].(string)
	c.environmentID, _ = pt["environmentId"].(string)
	if c.projectID == "" || c.environmentID == "" {
		return fmt.Errorf("token did not return projectId or environmentId")
	}
	return nil
}

// resolveServiceID looks up a service ID by name within the project.
func (c *Client) resolveServiceID(serviceName string) (string, error) {
	data, err := c.graphqlRequest(
		`query project($id: String!) {
			project(id: $id) {
				services {
					edges {
						node { id name }
					}
				}
			}
		}`,
		map[string]any{"id": c.projectID},
	)
	if err != nil {
		return "", fmt.Errorf("fetch project services: %w", err)
	}

	project, ok := data["project"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("project not found in response")
	}

	services, ok := project["services"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("services not found in response")
	}

	edges, ok := services["edges"].([]any)
	if !ok {
		return "", fmt.Errorf("service edges not found in response")
	}

	for _, edge := range edges {
		node, ok := edge.(map[string]any)
		if !ok {
			continue
		}
		node, ok = node["node"].(map[string]any)
		if !ok {
			continue
		}
		name, _ := node["name"].(string)
		if name == serviceName {
			id, _ := node["id"].(string)
			if id == "" {
				return "", fmt.Errorf("service %q found but has empty ID", serviceName)
			}
			return id, nil
		}
	}

	return "", fmt.Errorf("service %q not found in project", serviceName)
}

// GetVariables fetches all environment variables for a service as a map.
// References like ${{Service.VAR}} are returned as-is (unrendered).
func (c *Client) GetVariables(serviceName string) (map[string]string, error) {
	return c.getVariables(serviceName, true)
}

// GetVariablesRendered fetches all environment variables with references
// resolved to their actual values.
func (c *Client) GetVariablesRendered(serviceName string) (map[string]string, error) {
	return c.getVariables(serviceName, false)
}

func (c *Client) getVariables(serviceName string, unrendered bool) (map[string]string, error) {
	serviceID, err := c.resolveServiceID(serviceName)
	if err != nil {
		return nil, err
	}

	data, err := c.graphqlRequest(
		`query variables($projectId: String!, $environmentId: String!, $serviceId: String, $unrendered: Boolean) {
			variables(
				projectId: $projectId
				environmentId: $environmentId
				serviceId: $serviceId
				unrendered: $unrendered
			)
		}`,
		map[string]any{
			"projectId":     c.projectID,
			"environmentId": c.environmentID,
			"serviceId":     serviceID,
			"unrendered":    unrendered,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("fetch variables: %w", err)
	}

	varsRaw, ok := data["variables"]
	if !ok {
		return nil, fmt.Errorf("variables not found in response")
	}

	// Variables come back as a JSON object (map[string]any).
	varsBytes, err := json.Marshal(varsRaw)
	if err != nil {
		return nil, fmt.Errorf("re-marshal variables: %w", err)
	}

	vars := make(map[string]string)
	if err := json.Unmarshal(varsBytes, &vars); err != nil {
		return nil, fmt.Errorf("parse variables: %w", err)
	}
	return vars, nil
}

// SetVariable sets a single environment variable on a service.
// Uses skipDeploys=true so changes don't trigger a redeploy per variable.
func (c *Client) SetVariable(serviceName, key, value string) error {
	return c.SetVariables(serviceName, map[string]string{key: value})
}

// SetVariables sets multiple environment variables on a service in one
// atomic mutation. Uses skipDeploys=true — deploy is triggered separately
// by the caller after all variables are set.
func (c *Client) SetVariables(serviceName string, vars map[string]string) error {
	serviceID, err := c.resolveServiceID(serviceName)
	if err != nil {
		return err
	}

	_, err = c.graphqlRequest(
		`mutation variableCollectionUpsert($input: VariableCollectionUpsertInput!) {
			variableCollectionUpsert(input: $input)
		}`,
		map[string]any{
			"input": map[string]any{
				"projectId":     c.projectID,
				"environmentId": c.environmentID,
				"serviceId":     serviceID,
				"variables":     vars,
				"skipDeploys":   true,
			},
		},
	)
	if err != nil {
		return fmt.Errorf("set variables: %w", err)
	}
	return nil
}

// Deploy runs `railway up --service <name>` from the current directory.
// This uploads local files to Railway for building — works without a
// connected GitHub repo. Requires the Railway CLI binary on PATH.
// The command blocks until the deploy finishes and streams output directly.
func (c *Client) Deploy(serviceName string) error {
	cmd := exec.Command("railway", "up", "--service", serviceName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "RAILWAY_TOKEN="+c.token)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("railway up --service %s: %w", serviceName, err)
	}
	return nil
}
