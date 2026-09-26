package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type CICategory struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"-"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CIType struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"-"`
	CategoryID  uuid.UUID `json:"category_id"`
	AssetKind   string    `json:"asset_kind"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CMDBClassification struct {
	CategoryID   uuid.UUID `json:"category_id"`
	CategoryName string    `json:"category_name"`
	TypeID       uuid.UUID `json:"type_id"`
	TypeName     string    `json:"type_name"`
	Source       string    `json:"classification_source"`
}

type CMDBAsset struct {
	ID             uuid.UUID          `json:"id"`
	AssetKind      string             `json:"asset_kind"`
	Name           string             `json:"name"`
	Detail         *string            `json:"detail"`
	Status         string             `json:"status"`
	RiskTier       *string            `json:"risk_tier"`
	WorkspaceID    *uuid.UUID         `json:"workspace_id"`
	WorkspaceName  *string            `json:"workspace_name"`
	WorkspaceLabel string             `json:"workspace_label"`
	Classification CMDBClassification `json:"classification"`
}

type CMDBAssetFilter struct {
	OrgID            uuid.UUID
	IncludeEndpoints bool
	Kind             string
	CategoryID       *uuid.UUID
	TypeID           *uuid.UUID
	WorkspaceID      *uuid.UUID
	Query            string
	Limit            int
	Offset           int
}

type CMDBTypeCount struct {
	TypeID     uuid.UUID `json:"type_id"`
	CategoryID uuid.UUID `json:"category_id"`
	Count      int64     `json:"count"`
}

func scanCategory(row pgx.Row) (CICategory, error) {
	var v CICategory
	err := row.Scan(&v.ID, &v.OrgID, &v.Name, &v.Description, &v.SortOrder, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

func scanType(row pgx.Row) (CIType, error) {
	var v CIType
	err := row.Scan(&v.ID, &v.OrgID, &v.CategoryID, &v.AssetKind, &v.Name, &v.Description, &v.IsDefault, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

const categoryColumns = `id, org_id, name, description, sort_order, created_at, updated_at`
const typeColumns = `id, org_id, category_id, asset_kind, name, description, is_default, created_at, updated_at`

func (q *Queries) ListCICategories(ctx context.Context, orgID uuid.UUID) ([]CICategory, error) {
	rows, err := q.db.Query(ctx, `SELECT `+categoryColumns+` FROM ci_categories WHERE org_id=$1 ORDER BY sort_order, normalized_name, id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CICategory
	for rows.Next() {
		v, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (q *Queries) ListCITypes(ctx context.Context, orgID uuid.UUID) ([]CIType, error) {
	rows, err := q.db.Query(ctx, `SELECT `+typeColumns+` FROM ci_types WHERE org_id=$1 ORDER BY asset_kind, normalized_name, id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CIType
	for rows.Next() {
		v, err := scanType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (q *Queries) GetCICategory(ctx context.Context, orgID, id uuid.UUID) (CICategory, error) {
	return scanCategory(q.db.QueryRow(ctx, `SELECT `+categoryColumns+` FROM ci_categories WHERE id=$1 AND org_id=$2`, id, orgID))
}

func (q *Queries) GetCIType(ctx context.Context, orgID, id uuid.UUID) (CIType, error) {
	return scanType(q.db.QueryRow(ctx, `SELECT `+typeColumns+` FROM ci_types WHERE id=$1 AND org_id=$2`, id, orgID))
}

func (q *Queries) CreateCICategory(ctx context.Context, orgID uuid.UUID, name string, description *string, sortOrder int) (CICategory, error) {
	return scanCategory(q.db.QueryRow(ctx, `INSERT INTO ci_categories(org_id,name,normalized_name,description,sort_order) VALUES($1,$2,'', $3,$4) RETURNING `+categoryColumns, orgID, name, description, sortOrder))
}

func (q *Queries) UpdateCICategory(ctx context.Context, orgID, id uuid.UUID, name string, description *string, sortOrder int) (CICategory, error) {
	return scanCategory(q.db.QueryRow(ctx, `UPDATE ci_categories SET name=$3,description=$4,sort_order=$5 WHERE id=$1 AND org_id=$2 RETURNING `+categoryColumns, id, orgID, name, description, sortOrder))
}

func (q *Queries) DeleteCICategory(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := q.db.Exec(ctx, `DELETE FROM ci_categories WHERE id=$1 AND org_id=$2`, id, orgID)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (q *Queries) CreateCIType(ctx context.Context, orgID, categoryID uuid.UUID, kind, name string, description *string, makeDefault bool) (CIType, error) {
	if !makeDefault {
		return scanType(q.db.QueryRow(ctx, `INSERT INTO ci_types(org_id,category_id,asset_kind,name,normalized_name,description) VALUES($1,$2,$3,$4,'',$5) RETURNING `+typeColumns, orgID, categoryID, kind, name, description))
	}
	tx, err := q.Begin(ctx)
	if err != nil {
		return CIType{}, err
	}
	defer tx.Rollback(ctx)
	qt := q.WithTx(tx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':' || $2, 0))`, orgID, kind); err != nil {
		return CIType{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE ci_types SET is_default=FALSE WHERE org_id=$1 AND asset_kind=$2 AND is_default`, orgID, kind); err != nil {
		return CIType{}, err
	}
	v, err := scanType(qt.db.QueryRow(ctx, `INSERT INTO ci_types(org_id,category_id,asset_kind,name,normalized_name,description,is_default) VALUES($1,$2,$3,$4,'',$5,TRUE) RETURNING `+typeColumns, orgID, categoryID, kind, name, description))
	if err != nil {
		return CIType{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CIType{}, err
	}
	return v, nil
}

func (q *Queries) UpdateCIType(ctx context.Context, orgID, id, categoryID uuid.UUID, kind, name string, description *string, makeDefault bool) (CIType, error) {
	tx, err := q.Begin(ctx)
	if err != nil {
		return CIType{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':' || $2, 0))`, orgID, kind); err != nil {
		return CIType{}, err
	}
	if makeDefault {
		if _, err = tx.Exec(ctx, `UPDATE ci_types SET is_default=FALSE WHERE org_id=$1 AND asset_kind=$2 AND is_default`, orgID, kind); err != nil {
			return CIType{}, err
		}
	}
	v, err := scanType(tx.QueryRow(ctx, `UPDATE ci_types SET category_id=$3,name=$4,description=$5,is_default=CASE WHEN $6 THEN TRUE ELSE is_default END WHERE id=$1 AND org_id=$2 AND asset_kind=$7 RETURNING `+typeColumns, id, orgID, categoryID, name, description, makeDefault, kind))
	if err != nil {
		return CIType{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CIType{}, err
	}
	return v, nil
}

func (q *Queries) DeleteCIType(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := q.db.Exec(ctx, `DELETE FROM ci_types WHERE id=$1 AND org_id=$2 AND NOT is_default`, id, orgID)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

const cmdbAssetUnion = `
SELECT e.id,'endpoint'::text asset_kind,e.hostname name,
       COALESCE(e.agent_version,'') detail,'discovered'::text status,
       CASE WHEN e.risk_score >= 70 THEN 'high' WHEN e.risk_score >= 40 THEN 'medium' ELSE 'low' END risk_tier,
       e.workspace_id,w.name workspace_name,
       COALESCE(e.ci_type_id, d.id) type_id,
       CASE WHEN e.ci_type_id IS NULL THEN 'default' ELSE 'explicit' END classification_source
FROM endpoints e
LEFT JOIN workspaces w ON w.id=e.workspace_id AND w.org_id=e.org_id
JOIN ci_types d ON d.org_id=e.org_id AND d.asset_kind='endpoint' AND d.is_default
WHERE e.org_id=$1 AND $2
UNION ALL
SELECT a.id,'agent',a.name,a.model,a.status,a.risk_tier,a.workspace_id,w.name,
       COALESCE(a.ci_type_id,d.id),CASE WHEN a.ci_type_id IS NULL THEN 'default' ELSE 'explicit' END
FROM gateway_agents a
LEFT JOIN workspaces w ON w.id=a.workspace_id AND w.org_id=a.org_id
JOIN ci_types d ON d.org_id=a.org_id AND d.asset_kind='agent' AND d.is_default
WHERE a.org_id=$1
UNION ALL
SELECT t.id,'tool',t.name,t.type,t.status,NULL::text,NULL::uuid,NULL::text,
       COALESCE(t.ci_type_id,d.id),CASE WHEN t.ci_type_id IS NULL THEN 'default' ELSE 'explicit' END
FROM gateway_tools t
JOIN ci_types d ON d.org_id=t.org_id AND d.asset_kind='tool' AND d.is_default
WHERE t.org_id=$1`

func cmdbFilteredSQL(f CMDBAssetFilter) (string, []any) {
	args := []any{f.OrgID, f.IncludeEndpoints}
	where := []string{"TRUE"}
	add := func(expr string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(expr, len(args)))
	}
	if f.Kind != "" {
		add("a.asset_kind=$%d", f.Kind)
	}
	if f.CategoryID != nil {
		add("ct.category_id=$%d", *f.CategoryID)
	}
	if f.TypeID != nil {
		add("a.type_id=$%d", *f.TypeID)
	}
	if f.WorkspaceID != nil {
		add("a.workspace_id=$%d", *f.WorkspaceID)
	}
	if strings.TrimSpace(f.Query) != "" {
		args = append(args, strings.TrimSpace(f.Query))
		where = append(where, fmt.Sprintf("(a.name ILIKE '%%' || $%d || '%%' OR COALESCE(a.detail,'') ILIKE '%%' || $%d || '%%')", len(args), len(args)))
	}
	return `WITH a AS (` + cmdbAssetUnion + `), filtered AS (
SELECT a.*,ct.category_id,ct.name type_name,cc.name category_name
FROM a JOIN ci_types ct ON ct.id=a.type_id AND ct.org_id=$1
JOIN ci_categories cc ON cc.id=ct.category_id AND cc.org_id=$1
WHERE ` + strings.Join(where, " AND ") + `)`, args
}

func (q *Queries) ListCMDBAssets(ctx context.Context, f CMDBAssetFilter) ([]CMDBAsset, int64, error) {
	base, args := cmdbFilteredSQL(f)
	var total int64
	if err := q.db.QueryRow(ctx, base+` SELECT count(*) FROM filtered`, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	query := base + ` SELECT id,asset_kind,name,NULLIF(detail,''),status,risk_tier,workspace_id,workspace_name,
CASE WHEN asset_kind='tool' THEN 'not_workspace_scoped' WHEN workspace_id IS NULL THEN 'global_floor' ELSE 'workspace' END,
category_id,category_name,type_id,type_name,classification_source
FROM filtered ORDER BY lower(name),asset_kind,id LIMIT $` + fmt.Sprint(len(args)-1) + ` OFFSET $` + fmt.Sprint(len(args))
	rows, err := q.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []CMDBAsset
	for rows.Next() {
		var a CMDBAsset
		if err := rows.Scan(&a.ID, &a.AssetKind, &a.Name, &a.Detail, &a.Status, &a.RiskTier, &a.WorkspaceID, &a.WorkspaceName, &a.WorkspaceLabel, &a.Classification.CategoryID, &a.Classification.CategoryName, &a.Classification.TypeID, &a.Classification.TypeName, &a.Classification.Source); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

func (q *Queries) CountCMDBAssetsByType(ctx context.Context, f CMDBAssetFilter) ([]CMDBTypeCount, error) {
	base, args := cmdbFilteredSQL(f)
	rows, err := q.db.Query(ctx, base+` SELECT type_id,category_id,count(*) FROM filtered GROUP BY type_id,category_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CMDBTypeCount
	for rows.Next() {
		var c CMDBTypeCount
		if err := rows.Scan(&c.TypeID, &c.CategoryID, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (q *Queries) SetCMDBAssetType(ctx context.Context, orgID uuid.UUID, kind string, assetID uuid.UUID, typeID *uuid.UUID) (CMDBClassification, error) {
	queries := map[string]string{
		"endpoint": `UPDATE endpoints SET ci_type_id=$3 WHERE id=$1 AND org_id=$2`,
		"agent":    `UPDATE gateway_agents SET ci_type_id=$3 WHERE id=$1 AND org_id=$2`,
		"tool":     `UPDATE gateway_tools SET ci_type_id=$3 WHERE id=$1 AND org_id=$2`,
	}
	sql, ok := queries[kind]
	if !ok {
		return CMDBClassification{}, fmt.Errorf("invalid asset kind")
	}
	if typeID != nil {
		var exists bool
		if err := q.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ci_types WHERE id=$1 AND org_id=$2 AND asset_kind=$3)`, *typeID, orgID, kind).Scan(&exists); err != nil {
			return CMDBClassification{}, err
		}
		if !exists {
			return CMDBClassification{}, pgx.ErrNoRows
		}
	}
	tag, err := q.db.Exec(ctx, sql, assetID, orgID, typeID)
	if err != nil {
		return CMDBClassification{}, err
	}
	if tag.RowsAffected() == 0 {
		return CMDBClassification{}, pgx.ErrNoRows
	}
	var c CMDBClassification
	err = q.db.QueryRow(ctx, `SELECT c.id,c.name,t.id,t.name,CASE WHEN $3::uuid IS NULL THEN 'default' ELSE 'explicit' END FROM ci_types t JOIN ci_categories c ON c.id=t.category_id AND c.org_id=t.org_id WHERE t.org_id=$1 AND t.id=COALESCE($3,(SELECT id FROM ci_types WHERE org_id=$1 AND asset_kind=$2 AND is_default))`, orgID, kind, typeID).Scan(&c.CategoryID, &c.CategoryName, &c.TypeID, &c.TypeName, &c.Source)
	return c, err
}
