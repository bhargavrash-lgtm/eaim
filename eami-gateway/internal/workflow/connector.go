package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/toolrouter"
)

// ErrConnectorNotFound mirrors approval.Router's own "resolved connector
// no longer exists" case -- a step's gateway_tool_id was valid when the
// workflow was defined but the row is gone by the time this step tries
// to execute (deleted between definition and run, or between an earlier
// step and this one, mid-run).
var ErrConnectorNotFound = errors.New("workflow: resolved connector no longer exists")

// connectorRow is gateway_tools' security-relevant config for one row,
// read by ID -- deliberately duplicated from approval/router.go's own
// unexported resolvedConnectorConfig/fetchResolvedConnector rather than
// imported (that package's version is unexported, and this codebase's
// own established B-044/B-025 precedent is to duplicate small security-
// relevant helpers across packages rather than build shared
// infrastructure for them). The one addition beyond approval's version:
// Name, needed here (and not there) because this package must build a
// real mcp.ActionContext.Tool value -- dispatch() resolves by name, not
// ID, so the connector's name has to travel from this read into the
// ActionContext the executor builds.
type connectorRow struct {
	name                 string
	toolType             string
	authType             string
	baseURLOrProvider    string // base_url for rest_api, provider for ai_provider
	credentialsEncrypted []byte
	actionPaths          map[string]toolrouter.ActionPathEntry
}

// resolveConnectorByID reads gateway_tools by (id, org_id) -- never by
// name, and never trusting anything but the caller's own orgID (which
// always traces back to the authenticated agent that triggered the
// workflow run, never client input).
func resolveConnectorByID(ctx context.Context, pool *pgxpool.Pool, orgID, toolID uuid.UUID) (connectorRow, error) {
	var row connectorRow
	var baseURL, provider *string
	var actionPathsRaw []byte
	err := pool.QueryRow(ctx, `
		SELECT name, type, auth_type, base_url, provider, credentials_encrypted, action_paths
		FROM gateway_tools
		WHERE id = $1 AND org_id = $2
	`, toolID, orgID).Scan(&row.name, &row.toolType, &row.authType, &baseURL, &provider, &row.credentialsEncrypted, &actionPathsRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return connectorRow{}, ErrConnectorNotFound
		}
		return connectorRow{}, fmt.Errorf("workflow: resolve connector %s: %w", toolID, err)
	}
	switch row.toolType {
	case "rest_api":
		if baseURL != nil {
			row.baseURLOrProvider = *baseURL
		}
	case "ai_provider":
		if provider != nil {
			row.baseURLOrProvider = *provider
		}
	}
	if len(actionPathsRaw) > 0 {
		_ = json.Unmarshal(actionPathsRaw, &row.actionPaths)
	}
	return row, nil
}

// configHashJSON re-marshals actionPaths canonically (encoding/json.Marshal
// of a Go map always sorts keys), matching main.go's/approval.Router's own
// exact computation -- so this package's informational hash and B-057's
// real enforcement pin agree bit-for-bit for the same connector state,
// even though this value is never used for enforcement itself.
func (c connectorRow) configHashJSON() []byte {
	if len(c.actionPaths) == 0 {
		return nil
	}
	b, _ := json.Marshal(c.actionPaths)
	return b
}
