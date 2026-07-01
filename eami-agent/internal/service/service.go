// Package service handles Windows Service Control Manager integration for eami-agent.
package service

import "context"

// ServiceName is the Windows SCM service name.
const ServiceName = "EAMIAgent"

// DisplayName is the human-readable name shown in services.msc.
const DisplayName = "EAMI Agent"

// Description is the service description shown in services.msc.
const Description = "Enterprise AI Management Interface — endpoint discovery agent."

// RunFn is the function that runs the agent scan loop until ctx is cancelled.
type RunFn func(ctx context.Context)
