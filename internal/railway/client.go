// Package railway is a thin wrapper around the Railway GraphQL API.
// It uses project tokens to manage variables and trigger deploys.
package railway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
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

// Deploy triggers a deployment for the service, waits for it to complete,
// and streams the runtime logs. Returns an error if the deploy fails.
func (c *Client) Deploy(serviceName string) error {
	deploymentID, err := c.triggerDeploy(serviceName)
	if err != nil {
		return err
	}
	return c.waitForDeployment(serviceName, deploymentID)
}

// DeployDetached triggers a deployment and returns immediately without
// waiting for it to complete.
func (c *Client) DeployDetached(serviceName string) error {
	_, err := c.triggerDeploy(serviceName)
	return err
}

// triggerDeploy triggers a deployment and returns the deployment ID.
func (c *Client) triggerDeploy(serviceName string) (string, error) {
	serviceID, err := c.resolveServiceID(serviceName)
	if err != nil {
		return "", err
	}

	data, err := c.graphqlRequest(
		`mutation serviceInstanceDeployV2($serviceId: String!, $environmentId: String!) {
			serviceInstanceDeployV2(serviceId: $serviceId, environmentId: $environmentId)
		}`,
		map[string]any{
			"serviceId":     serviceID,
			"environmentId": c.environmentID,
		},
	)
	if err != nil {
		return "", fmt.Errorf("deploy service %s: %w", serviceName, err)
	}

	deploymentID, ok := data["serviceInstanceDeployV2"].(string)
	if !ok || deploymentID == "" {
		return "", fmt.Errorf("deploy service %s: no deployment ID returned (response: %v)", serviceName, data)
	}
	return deploymentID, nil
}

// waitForDeployment polls the deployment status until it reaches a terminal
// state (SUCCESS, FAILED, CRASHED, SKIPPED). It streams runtime logs as they
// become available. Returns an error if the deploy fails.
//
// If the initial deployment is REMOVED (superseded by a newer one), it fetches
// the latest deployment for the service and polls that instead.
func (c *Client) waitForDeployment(serviceName, deploymentID string) error {
	const pollInterval = 5 * time.Second

	// Terminal statuses — once reached, stop polling.
	terminal := map[string]bool{
		"SUCCESS": true,
		"FAILED":  true,
		"CRASHED": true,
		"SKIPPED": true,
	}

	var lastStatus string
	logsFetched := 0
	currentID := deploymentID

	for {
		data, err := c.graphqlRequest(
			`query deployment($id: String!) {
				deployment(id: $id) {
					id
					status
				}
			}`,
			map[string]any{"id": currentID},
		)
		if err != nil {
			return fmt.Errorf("fetch deployment status: %w", err)
		}

		deployment, ok := data["deployment"].(map[string]any)
		if !ok {
			return fmt.Errorf("deployment not found in response")
		}

		status, _ := deployment["status"].(string)

		// If REMOVED, the deployment was superseded — fetch the latest one.
		if status == "REMOVED" {
			latestID, err := c.getLatestDeploymentID(serviceName)
			if err != nil {
				return fmt.Errorf("deployment %s was REMOVED and no active deployment found: %w", currentID, err)
			}
			if latestID == currentID {
				// Still the same — wait and retry, the new one may not exist yet.
				time.Sleep(pollInterval)
				continue
			}
			currentID = latestID
			lastStatus = ""
			logsFetched = 0
			fmt.Printf("=== Switched to latest deployment: %s ===\n", currentID)
			continue
		}

		if status != lastStatus {
			fmt.Printf("=== Deployment status: %s ===\n", status)
			lastStatus = status
		}

		// Fetch and print any new runtime logs once the deployment is running.
		if status == "DEPLOYING" || status == "SUCCESS" || status == "FAILED" || status == "CRASHED" {
			logs, err := c.getDeploymentLogs(currentID, logsFetched)
			if err == nil && len(logs) > logsFetched {
				for _, l := range logs[logsFetched:] {
					fmt.Printf("[%s] %s\n", l.severity, l.message)
				}
				logsFetched = len(logs)
			}
		}

		if terminal[status] {
			if status != "SUCCESS" {
				// Fetch any remaining logs on failure.
				logs, err := c.getDeploymentLogs(currentID, logsFetched)
				if err == nil && len(logs) > logsFetched {
					for _, l := range logs[logsFetched:] {
						fmt.Printf("[%s] %s\n", l.severity, l.message)
					}
				}
				return fmt.Errorf("deployment %s ended with status %s", currentID, status)
			}
			return nil
		}

		time.Sleep(pollInterval)
	}
}

// getLatestDeploymentID fetches the most recent deployment ID for a service.
func (c *Client) getLatestDeploymentID(serviceName string) (string, error) {
	serviceID, err := c.resolveServiceID(serviceName)
	if err != nil {
		return "", err
	}

	data, err := c.graphqlRequest(
		`query deployments($input: DeploymentListInput!, $first: Int) {
			deployments(input: $input, first: $first) {
				edges {
					node {
						id
						status
						createdAt
					}
				}
			}
		}`,
		map[string]any{
			"input": map[string]any{
				"projectId":     c.projectID,
				"serviceId":     serviceID,
				"environmentId": c.environmentID,
			},
			"first": 1,
		},
	)
	if err != nil {
		return "", fmt.Errorf("fetch deployments: %w", err)
	}

	deployments, ok := data["deployments"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("deployments not found in response")
	}

	edges, ok := deployments["edges"].([]any)
	if !ok || len(edges) == 0 {
		return "", fmt.Errorf("no deployments found")
	}

	edge, ok := edges[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid deployment edge")
	}

	node, ok := edge["node"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid deployment node")
	}

	id, _ := node["id"].(string)
	if id == "" {
		return "", fmt.Errorf("deployment has empty ID")
	}
	return id, nil
}

type deploymentLog struct {
	timestamp string
	message   string
	severity  string
}

// getDeploymentLogs fetches runtime logs for a deployment.
func (c *Client) getDeploymentLogs(deploymentID string, limit int) ([]deploymentLog, error) {
	data, err := c.graphqlRequest(
		`query deploymentLogs($deploymentId: String!, $limit: Int) {
			deploymentLogs(deploymentId: $deploymentId, limit: $limit) {
				timestamp
				message
				severity
			}
		}`,
		map[string]any{
			"deploymentId": deploymentID,
			"limit":        500,
		},
	)
	if err != nil {
		return nil, err
	}

	logsRaw, ok := data["deploymentLogs"].([]any)
	if !ok {
		return nil, nil
	}

	logs := make([]deploymentLog, 0, len(logsRaw))
	for _, l := range logsRaw {
		m, ok := l.(map[string]any)
		if !ok {
			continue
		}
		logs = append(logs, deploymentLog{
			timestamp: m["timestamp"].(string),
			message:   m["message"].(string),
			severity:  m["severity"].(string),
		})
	}
	return logs, nil
}
