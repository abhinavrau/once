package docker

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

const ContainerLogMaxSize = "10m"

func ContainerLogConfig() container.LogConfig {
	return container.LogConfig{
		Type: "json-file",
		Config: map[string]string{
			"max-size": ContainerLogMaxSize,
			"max-file": "1",
		},
	}
}

type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func execInContainer(ctx context.Context, c *client.Client, containerName string, cmd []string) (ExecResult, error) {
	execResp, err := c.ContainerExecCreate(ctx, containerName, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("creating exec: %w", describeExecCreateFailure(ctx, c, containerName, err))
	}

	resp, err := c.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("attaching exec: %w", err)
	}
	defer resp.Close()

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, resp.Reader); err != nil {
		return ExecResult{}, fmt.Errorf("reading exec output: %w", err)
	}

	inspect, err := c.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return ExecResult{}, fmt.Errorf("inspecting exec: %w", err)
	}

	return ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: inspect.ExitCode,
	}, nil
}

// Helpers

// describeExecCreateFailure enriches a failed exec creation against a dead or
// restart-looping container with its state and recent logs, so callers report
// why the container is down (e.g. a crash-looping tailscale sidecar's "invalid
// key") instead of Docker's bare "container is restarting". Any other failure
// passes through unchanged.
func describeExecCreateFailure(ctx context.Context, c *client.Client, containerName string, err error) error {
	info, inspectErr := c.ContainerInspect(ctx, containerName)
	if inspectErr != nil || info.State == nil || (info.State.Running && !info.State.Restarting) {
		return err
	}
	msg := fmt.Sprintf("container %s is %s (exit code %d)", containerName, info.State.Status, info.State.ExitCode)
	return fmt.Errorf("%w: %s", err, withRecentLogs(ctx, c, containerName, msg))
}

// withRecentLogs appends a container's recent log tail to msg, for errors
// explaining why a container is down.
func withRecentLogs(ctx context.Context, c *client.Client, containerName, msg string) string {
	if logs := tailContainerLogs(ctx, c, containerName, 20); logs != "" {
		msg += "; recent logs:\n" + logs
	}
	return msg
}

// tailContainerLogs returns the last lines of a container's combined output,
// for embedding in errors about a container that died. Best effort: any
// failure reads as no logs.
func tailContainerLogs(ctx context.Context, c *client.Client, containerName string, lines int) string {
	reader, err := c.ContainerLogs(ctx, containerName, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       strconv.Itoa(lines),
	})
	if err != nil {
		return ""
	}
	defer reader.Close()

	var buf bytes.Buffer
	_, _ = stdcopy.StdCopy(&buf, &buf, reader)
	return strings.TrimSpace(buf.String())
}
