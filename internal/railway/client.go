// Package railway is a thin wrapper around the Railway CLI (`railway`).
// It shells out to the CLI for variable management and deploys, keeping the
// Go code testable while avoiding a custom GraphQL client.
package railway

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// Client wraps the Railway CLI, authenticated via RAILWAY_TOKEN.
type Client struct {
	token string
}

// NewClient returns a Client that authenticates to Railway with the given token.
func NewClient(token string) *Client {
	return &Client{token: token}
}

func (c *Client) cmd(args ...string) *exec.Cmd {
	cmd := exec.Command("railway", args...)
	cmd.Env = append(os.Environ(), "RAILWAY_TOKEN="+c.token)
	return cmd
}

// GetVariables fetches all environment variables for a service as a map.
func (c *Client) GetVariables(serviceName string) (map[string]string, error) {
	cmd := c.cmd("variables", "--service", serviceName, "--json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("railway variables --service %s: %w", serviceName, err)
	}

	vars := make(map[string]string)
	if err := json.Unmarshal(output, &vars); err != nil {
		return nil, fmt.Errorf("parse variables JSON: %w", err)
	}
	return vars, nil
}

// SetVariable sets a single environment variable on a service.
func (c *Client) SetVariable(serviceName, key, value string) error {
	cmd := c.cmd("variables", "set", "--service", serviceName, fmt.Sprintf("%s=%s", key, value))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("railway variables set %s: %w: %s", key, err, string(output))
	}
	return nil
}

// Deploy builds and deploys a service from the current directory using `railway up`.
// The working directory must contain the source code (Dockerfile, etc.).
func (c *Client) Deploy(serviceName string) error {
	cmd := c.cmd("up", "--service", serviceName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("railway up --service %s: %w", serviceName, err)
	}
	return nil
}
