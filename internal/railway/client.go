// Package railway is a thin wrapper around the Railway GraphQL API.
// It uses project tokens to manage variables and trigger deploys.
package railway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const graphqlEndpoint = "https://backboard.railway.com/graphql/v2"

// Client wraps the Railway GraphQL API, authenticated via RAILWAY_TOKEN.
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
// This must be called before any variable or deploy operations.
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
			"unrendered":    true,
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
	serviceID, err := c.resolveServiceID(serviceName)
	if err != nil {
		return err
	}

	_, err = c.graphqlRequest(
		`mutation variableUpsert($input: VariableUpsertInput!) {
			variableUpsert(input: $input)
		}`,
		map[string]any{
			"input": map[string]any{
				"projectId":     c.projectID,
				"environmentId": c.environmentID,
				"serviceId":     serviceID,
				"name":          key,
				"value":         value,
				"skipDeploys":   true,
			},
		},
	)
	if err != nil {
		return fmt.Errorf("set variable %s: %w", key, err)
	}
	return nil
}

// Deploy triggers a deployment for the service.
func (c *Client) Deploy(serviceName string) error {
	serviceID, err := c.resolveServiceID(serviceName)
	if err != nil {
		return err
	}

	_, err = c.graphqlRequest(
		`mutation serviceInstanceDeploy($serviceId: String!, $environmentId: String!) {
			serviceInstanceDeploy(serviceId: $serviceId, environmentId: $environmentId)
		}`,
		map[string]any{
			"serviceId":     serviceID,
			"environmentId": c.environmentID,
		},
	)
	if err != nil {
		return fmt.Errorf("deploy service %s: %w", serviceName, err)
	}
	return nil
}
