package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/eami/api/internal/license"
	"github.com/eami/api/internal/store"
)

type cmdbTypeResp struct {
	store.CIType
	AssetCount int64 `json:"asset_count"`
}

type cmdbCategoryResp struct {
	store.CICategory
	AssetCount int64          `json:"asset_count"`
	Types      []cmdbTypeResp `json:"types"`
}

type cmdbCategoryWrite struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	SortOrder   int     `json:"sort_order"`
}

type cmdbTypeWrite struct {
	CategoryID  uuid.UUID `json:"category_id"`
	AssetKind   string    `json:"asset_kind"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	IsDefault   bool      `json:"is_default"`
}

func validCMDBKind(kind string) bool { return kind == "endpoint" || kind == "agent" || kind == "tool" }

func (s *Server) discoveryLicensed(r *http.Request, orgID uuid.UUID) bool {
	row, err := s.queries.GetLatestLicense(r.Context(), orgID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("cmdb: license lookup failed", "org_id", orgID, "err", err)
		}
		return false
	}
	claims, err := license.Verify(row.RawLicense)
	return err == nil && claims.OrgID() == orgID.String() && claims.HasModule("discovery")
}

func parseOptionalUUID(raw string) (*uuid.UUID, error) {
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (s *Server) cmdbFilter(r *http.Request, includeEndpoints bool) (store.CMDBAssetFilter, error) {
	uc := claimsFromContext(r)
	q := r.URL.Query()
	f := store.CMDBAssetFilter{OrgID: uc.OrgID, IncludeEndpoints: includeEndpoints, Kind: q.Get("kind"), Query: q.Get("q")}
	if f.Kind != "" && !validCMDBKind(f.Kind) {
		return f, errors.New("kind must be endpoint, agent, or tool")
	}
	if len(f.Query) > 200 {
		return f, errors.New("q must be at most 200 characters")
	}
	var err error
	if f.CategoryID, err = parseOptionalUUID(q.Get("category_id")); err != nil {
		return f, errors.New("invalid category_id")
	}
	if f.TypeID, err = parseOptionalUUID(q.Get("type_id")); err != nil {
		return f, errors.New("invalid type_id")
	}
	if f.WorkspaceID, err = parseOptionalUUID(q.Get("workspace_id")); err != nil {
		return f, errors.New("invalid workspace_id")
	}
	return f, nil
}

func (s *Server) ListCMDBClassifications(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	licensed := s.discoveryLicensed(r, uc.OrgID)
	categories, err := s.queries.ListCICategories(r.Context(), uc.OrgID)
	if err != nil {
		writeError(w, 500, "internal_error", "failed to list classifications")
		return
	}
	types, err := s.queries.ListCITypes(r.Context(), uc.OrgID)
	if err != nil {
		writeError(w, 500, "internal_error", "failed to list classifications")
		return
	}
	counts, err := s.queries.CountCMDBAssetsByType(r.Context(), store.CMDBAssetFilter{OrgID: uc.OrgID, IncludeEndpoints: licensed})
	if err != nil {
		writeError(w, 500, "internal_error", "failed to count classified assets")
		return
	}
	countByType := map[uuid.UUID]int64{}
	for _, c := range counts {
		countByType[c.TypeID] = c.Count
	}
	byCategory := map[uuid.UUID][]cmdbTypeResp{}
	for _, t := range types {
		byCategory[t.CategoryID] = append(byCategory[t.CategoryID], cmdbTypeResp{CIType: t, AssetCount: countByType[t.ID]})
	}
	data := make([]cmdbCategoryResp, 0, len(categories))
	for _, c := range categories {
		item := cmdbCategoryResp{CICategory: c, Types: byCategory[c.ID]}
		if item.Types == nil {
			item.Types = []cmdbTypeResp{}
		}
		for _, t := range item.Types {
			item.AssetCount += t.AssetCount
		}
		data = append(data, item)
	}
	writeJSON(w, 200, map[string]any{"data": data, "endpoint_inventory_available": licensed})
}

func (s *Server) ListCMDBAssets(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	licensed := s.discoveryLicensed(r, uc.OrgID)
	f, err := s.cmdbFilter(r, licensed)
	if err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	if f.Kind == "endpoint" && !licensed {
		writeError(w, 403, "module_not_licensed", "your organization is not licensed for the discovery module")
		return
	}
	page, perPage := pagination(r.URL.Query().Get("page"), r.URL.Query().Get("per_page"), 25, 100)
	f.Limit = perPage
	f.Offset = (page - 1) * perPage
	assets, total, err := s.queries.ListCMDBAssets(r.Context(), f)
	if err != nil {
		writeError(w, 500, "internal_error", "failed to list CMDB assets")
		return
	}
	if assets == nil {
		assets = []store.CMDBAsset{}
	}
	counts, err := s.queries.CountCMDBAssetsByType(r.Context(), f)
	if err != nil {
		writeError(w, 500, "internal_error", "failed to count CMDB assets")
		return
	}
	if counts == nil {
		counts = []store.CMDBTypeCount{}
	}
	writeJSON(w, 200, map[string]any{"data": assets, "meta": PaginationMeta{Total: total, Page: page, PerPage: perPage}, "counts": counts, "endpoint_inventory_available": licensed})
}

func decodeCMDBWrite(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, 400, "bad_request", "invalid JSON body")
		return false
	}
	return true
}
func validateCMDBName(w http.ResponseWriter, name string) bool {
	if strings.TrimSpace(name) == "" || len(name) > 120 {
		writeError(w, 400, "bad_request", "name must be between 1 and 120 characters")
		return false
	}
	return true
}

func validateCMDBDescription(w http.ResponseWriter, description *string) bool {
	if description != nil && len(*description) > 500 {
		writeError(w, 400, "bad_request", "description must be at most 500 characters")
		return false
	}
	return true
}

func cmdbWriteError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "classification not found")
		return
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505":
			writeError(w, 409, "conflict", "a classification with that normalized name already exists")
			return
		case "23503":
			writeError(w, 409, "conflict", "classification is still in use or its parent does not exist")
			return
		case "23514":
			writeError(w, 409, "conflict", pe.Message)
			return
		}
	}
	writeError(w, 500, "internal_error", "classification update failed")
}

func (s *Server) CreateCMDBCategory(w http.ResponseWriter, r *http.Request) {
	var b cmdbCategoryWrite
	if !decodeCMDBWrite(w, r, &b) || !validateCMDBName(w, b.Name) || !validateCMDBDescription(w, b.Description) {
		return
	}
	uc := claimsFromContext(r)
	v, err := s.queries.CreateCICategory(r.Context(), uc.OrgID, b.Name, b.Description, b.SortOrder)
	if err != nil {
		cmdbWriteError(w, err)
		return
	}
	slog.Info("cmdb taxonomy changed", "org_id", uc.OrgID, "user_id", uc.UserID, "action", "category.created", "target_id", v.ID)
	writeJSON(w, 201, cmdbCategoryResp{CICategory: v, Types: []cmdbTypeResp{}})
}

func (s *Server) UpdateCMDBCategory(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "categoryId"))
	if err != nil {
		writeError(w, 400, "bad_request", "invalid categoryId")
		return
	}
	var b cmdbCategoryWrite
	if !decodeCMDBWrite(w, r, &b) || !validateCMDBName(w, b.Name) || !validateCMDBDescription(w, b.Description) {
		return
	}
	uc := claimsFromContext(r)
	v, err := s.queries.UpdateCICategory(r.Context(), uc.OrgID, id, b.Name, b.Description, b.SortOrder)
	if err != nil {
		cmdbWriteError(w, err)
		return
	}
	slog.Info("cmdb taxonomy changed", "org_id", uc.OrgID, "user_id", uc.UserID, "action", "category.updated", "target_id", v.ID)
	writeJSON(w, 200, cmdbCategoryResp{CICategory: v, Types: []cmdbTypeResp{}})
}

func (s *Server) DeleteCMDBCategory(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "categoryId"))
	if err != nil {
		writeError(w, 400, "bad_request", "invalid categoryId")
		return
	}
	uc := claimsFromContext(r)
	if err = s.queries.DeleteCICategory(r.Context(), uc.OrgID, id); err != nil {
		cmdbWriteError(w, err)
		return
	}
	slog.Info("cmdb taxonomy changed", "org_id", uc.OrgID, "user_id", uc.UserID, "action", "category.deleted", "target_id", id)
	w.WriteHeader(204)
}

func validateCMDBType(w http.ResponseWriter, b cmdbTypeWrite) bool {
	if !validateCMDBName(w, b.Name) {
		return false
	}
	if !validateCMDBDescription(w, b.Description) {
		return false
	}
	if b.CategoryID == uuid.Nil {
		writeError(w, 400, "bad_request", "category_id is required")
		return false
	}
	if !validCMDBKind(b.AssetKind) {
		writeError(w, 400, "bad_request", "asset_kind must be endpoint, agent, or tool")
		return false
	}
	return true
}

func (s *Server) CreateCMDBType(w http.ResponseWriter, r *http.Request) {
	var b cmdbTypeWrite
	if !decodeCMDBWrite(w, r, &b) || !validateCMDBType(w, b) {
		return
	}
	uc := claimsFromContext(r)
	v, err := s.queries.CreateCIType(r.Context(), uc.OrgID, b.CategoryID, b.AssetKind, b.Name, b.Description, b.IsDefault)
	if err != nil {
		cmdbWriteError(w, err)
		return
	}
	slog.Info("cmdb taxonomy changed", "org_id", uc.OrgID, "user_id", uc.UserID, "action", "type.created", "target_id", v.ID)
	writeJSON(w, 201, cmdbTypeResp{CIType: v})
}

func (s *Server) UpdateCMDBType(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "typeId"))
	if err != nil {
		writeError(w, 400, "bad_request", "invalid typeId")
		return
	}
	var b cmdbTypeWrite
	if !decodeCMDBWrite(w, r, &b) || !validateCMDBType(w, b) {
		return
	}
	uc := claimsFromContext(r)
	current, err := s.queries.GetCIType(r.Context(), uc.OrgID, id)
	if err != nil {
		cmdbWriteError(w, err)
		return
	}
	if current.AssetKind != b.AssetKind {
		writeError(w, 400, "bad_request", "asset_kind is immutable")
		return
	}
	v, err := s.queries.UpdateCIType(r.Context(), uc.OrgID, id, b.CategoryID, b.AssetKind, b.Name, b.Description, b.IsDefault)
	if err != nil {
		cmdbWriteError(w, err)
		return
	}
	slog.Info("cmdb taxonomy changed", "org_id", uc.OrgID, "user_id", uc.UserID, "action", "type.updated", "target_id", v.ID)
	writeJSON(w, 200, cmdbTypeResp{CIType: v})
}

func (s *Server) DeleteCMDBType(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "typeId"))
	if err != nil {
		writeError(w, 400, "bad_request", "invalid typeId")
		return
	}
	uc := claimsFromContext(r)
	v, err := s.queries.GetCIType(r.Context(), uc.OrgID, id)
	if err != nil {
		cmdbWriteError(w, err)
		return
	}
	if v.IsDefault {
		writeError(w, 409, "conflict", "the default type cannot be deleted")
		return
	}
	if err = s.queries.DeleteCIType(r.Context(), uc.OrgID, id); err != nil {
		cmdbWriteError(w, err)
		return
	}
	slog.Info("cmdb taxonomy changed", "org_id", uc.OrgID, "user_id", uc.UserID, "action", "type.deleted", "target_id", id)
	w.WriteHeader(204)
}

func (s *Server) SetCMDBAssetClassification(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "assetKind")
	if !validCMDBKind(kind) {
		writeError(w, 400, "bad_request", "assetKind must be endpoint, agent, or tool")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "assetId"))
	if err != nil {
		writeError(w, 400, "bad_request", "invalid assetId")
		return
	}
	var raw map[string]json.RawMessage
	if !decodeCMDBWrite(w, r, &raw) {
		return
	}
	encoded, present := raw["ci_type_id"]
	if !present {
		writeError(w, 400, "bad_request", "ci_type_id is required (use null to reset)")
		return
	}
	var typeID *uuid.UUID
	if string(encoded) != "null" {
		var rawID string
		if err := json.Unmarshal(encoded, &rawID); err != nil {
			writeError(w, 400, "bad_request", "ci_type_id must be a UUID or null")
			return
		}
		v, e := uuid.Parse(rawID)
		if e != nil {
			writeError(w, 400, "bad_request", "ci_type_id must be a UUID or null")
			return
		}
		typeID = &v
	}
	uc := claimsFromContext(r)
	c, err := s.queries.SetCMDBAssetType(r.Context(), uc.OrgID, kind, id, typeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found", "asset or classification not found")
			return
		}
		cmdbWriteError(w, err)
		return
	}
	slog.Info("cmdb asset classification changed", "org_id", uc.OrgID, "user_id", uc.UserID, "asset_kind", kind, "asset_id", id, "type_id", typeID)
	writeJSON(w, 200, c)
}
