//go:build sqlite || sqliteonly

package sqlitestore

import (
	"encoding/json"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// providerRow is a scan struct for llm_providers rows.
// Uses sqliteTime for created_at/updated_at to handle SQLite text timestamps.
type providerRow struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	Name            string          `json:"name" db:"name"`
	DisplayName     string          `json:"display_name" db:"display_name"`
	ProviderType    string          `json:"provider_type" db:"provider_type"`
	APIBase         string          `json:"api_base" db:"api_base"`
	APIKey          string          `json:"api_key" db:"api_key"`
	Enabled         bool            `json:"enabled" db:"enabled"`
	Settings        sqliteJSONValue `json:"settings" db:"settings"`
	WireAPI         string          `json:"wire_api" db:"wire_api"`
	AuthKind        string          `json:"auth_kind" db:"auth_kind"`
	ExecPath        *string         `json:"exec_path" db:"exec_path"`
	SettingsVersion int             `json:"settings_version" db:"settings_version"`
	CreatedAt       sqliteTime      `json:"created_at" db:"created_at"`
	UpdatedAt       sqliteTime      `json:"updated_at" db:"updated_at"`
	TenantID        uuid.UUID       `json:"tenant_id" db:"tenant_id"`
}

func (r *providerRow) toLLMProviderData() store.LLMProviderData {
	return store.LLMProviderData{
		BaseModel:       store.BaseModel{ID: r.ID, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time},
		TenantID:        r.TenantID,
		Name:            r.Name,
		DisplayName:     r.DisplayName,
		ProviderType:    r.ProviderType,
		APIBase:         r.APIBase,
		APIKey:          r.APIKey,
		Enabled:         r.Enabled,
		Settings:        json.RawMessage(r.Settings),
		WireAPI:         r.WireAPI,
		AuthKind:        r.AuthKind,
		ExecPath:        derefStr(r.ExecPath),
		SettingsVersion: r.SettingsVersion,
	}
}

// llmModelRow is a scan struct for llm_models rows.
// JSON columns use sqliteJSONValue (json.RawMessage has no Scan method, so the
// driver's TEXT value cannot be read into it directly) and timestamps use the
// sqliteTime/nullSqliteTime adapters.
type llmModelRow struct {
	ID                uuid.UUID       `json:"id" db:"id"`
	ProviderID        uuid.UUID       `json:"provider_id" db:"provider_id"`
	ModelID           string          `json:"model_id" db:"model_id"`
	DisplayName       *string         `json:"display_name" db:"display_name"`
	WireAPI           *string         `json:"wire_api" db:"wire_api"`
	ContextWindow     *int            `json:"context_window" db:"context_window"`
	MaxTokens         *int            `json:"max_tokens" db:"max_tokens"`
	MaxContextWindow  *int            `json:"max_context_window" db:"max_context_window"`
	CostInput         *float64        `json:"cost_input" db:"cost_input"`
	CostOutput        *float64        `json:"cost_output" db:"cost_output"`
	CostCacheRead     *float64        `json:"cost_cache_read" db:"cost_cache_read"`
	CostCacheWrite    *float64        `json:"cost_cache_write" db:"cost_cache_write"`
	Modalities        sqliteJSONValue `json:"modalities" db:"modalities"`
	Capabilities      sqliteJSONValue `json:"capabilities" db:"capabilities"`
	Reasoning         sqliteJSONValue `json:"reasoning" db:"reasoning"`
	Tokenizer         *string         `json:"tokenizer" db:"tokenizer"`
	Compat            sqliteJSONValue `json:"compat" db:"compat"`
	Source            string          `json:"source" db:"source"`
	Authoritative     bool            `json:"authoritative" db:"authoritative"`
	FetchedAt         nullSqliteTime  `json:"fetched_at" db:"fetched_at"`
	StaticFingerprint *string         `json:"static_fingerprint" db:"static_fingerprint"`
	Enabled           bool            `json:"enabled" db:"enabled"`
	CreatedAt         sqliteTime      `json:"created_at" db:"created_at"`
	UpdatedAt         sqliteTime      `json:"updated_at" db:"updated_at"`
}

func (r *llmModelRow) toLLMModel() store.LLMModel {
	model := store.LLMModel{
		BaseModel:         store.BaseModel{ID: r.ID, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time},
		ProviderID:        r.ProviderID,
		ModelID:           r.ModelID,
		DisplayName:       r.DisplayName,
		WireAPI:           r.WireAPI,
		ContextWindow:     r.ContextWindow,
		MaxTokens:         r.MaxTokens,
		MaxContextWindow:  r.MaxContextWindow,
		CostInput:         r.CostInput,
		CostOutput:        r.CostOutput,
		CostCacheRead:     r.CostCacheRead,
		CostCacheWrite:    r.CostCacheWrite,
		Modalities:        json.RawMessage(r.Modalities),
		Capabilities:      json.RawMessage(r.Capabilities),
		Reasoning:         json.RawMessage(r.Reasoning),
		Tokenizer:         r.Tokenizer,
		Compat:            json.RawMessage(r.Compat),
		Source:            r.Source,
		Authoritative:     r.Authoritative,
		StaticFingerprint: r.StaticFingerprint,
		Enabled:           r.Enabled,
	}
	if r.FetchedAt.Valid {
		fetchedAt := r.FetchedAt.Time
		model.FetchedAt = &fetchedAt
	}
	return model
}

// providerQuirkRow is a scan struct for provider_quirks rows.
type providerQuirkRow struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	TenantID       *uuid.UUID      `json:"tenant_id" db:"tenant_id"`
	WireAPI        string          `json:"wire_api" db:"wire_api"`
	EndpointFamily *string         `json:"endpoint_family" db:"endpoint_family"`
	ModelPattern   *string         `json:"model_pattern" db:"model_pattern"`
	Compat         sqliteJSONValue `json:"compat" db:"compat"`
	Note           *string         `json:"note" db:"note"`
	Source         string          `json:"source" db:"source"`
	Enabled        bool            `json:"enabled" db:"enabled"`
	CreatedAt      sqliteTime      `json:"created_at" db:"created_at"`
	UpdatedAt      sqliteTime      `json:"updated_at" db:"updated_at"`
}

func (r *providerQuirkRow) toProviderQuirk() store.ProviderQuirk {
	return store.ProviderQuirk{
		BaseModel:      store.BaseModel{ID: r.ID, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time},
		TenantID:       r.TenantID,
		WireAPI:        r.WireAPI,
		EndpointFamily: r.EndpointFamily,
		ModelPattern:   r.ModelPattern,
		Compat:         json.RawMessage(r.Compat),
		Note:           r.Note,
		Source:         r.Source,
		Enabled:        r.Enabled,
	}
}

// tenantRow is a scan struct for tenants rows.
type tenantRow struct {
	ID        uuid.UUID       `json:"id" db:"id"`
	Name      string          `json:"name" db:"name"`
	Slug      string          `json:"slug" db:"slug"`
	Status    string          `json:"status" db:"status"`
	Settings  sqliteJSONValue `json:"settings" db:"settings"`
	CreatedAt sqliteTime      `json:"created_at" db:"created_at"`
	UpdatedAt sqliteTime      `json:"updated_at" db:"updated_at"`
}

func (r *tenantRow) toTenantData() store.TenantData {
	return store.TenantData{
		ID:        r.ID,
		Name:      r.Name,
		Slug:      r.Slug,
		Status:    r.Status,
		Settings:  json.RawMessage(r.Settings),
		CreatedAt: r.CreatedAt.Time,
		UpdatedAt: r.UpdatedAt.Time,
	}
}

// tenantUserRow is a scan struct for tenant_users rows.
type tenantUserRow struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	TenantID    uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	UserID      string          `json:"user_id" db:"user_id"`
	DisplayName *string         `json:"display_name" db:"display_name"`
	Role        string          `json:"role" db:"role"`
	Metadata    sqliteJSONValue `json:"metadata" db:"metadata"`
	CreatedAt   sqliteTime      `json:"created_at" db:"created_at"`
	UpdatedAt   sqliteTime      `json:"updated_at" db:"updated_at"`
}

func (r *tenantUserRow) toTenantUserData() store.TenantUserData {
	return store.TenantUserData{
		ID:          r.ID,
		TenantID:    r.TenantID,
		UserID:      r.UserID,
		DisplayName: r.DisplayName,
		Role:        r.Role,
		Metadata:    json.RawMessage(r.Metadata),
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}
}

// mcpServerRow is a scan struct for mcp_servers rows.
// Pointer fields handle nullable columns that sqlx maps to empty string otherwise.
type mcpServerRow struct {
	ID                     uuid.UUID       `json:"id" db:"id"`
	Name                   string          `json:"name" db:"name"`
	DisplayName            *string         `json:"display_name" db:"display_name"`
	Transport              string          `json:"transport" db:"transport"`
	Command                *string         `json:"command" db:"command"`
	Args                   sqliteJSONValue `json:"args" db:"args"`
	URL                    *string         `json:"url" db:"url"`
	Headers                sqliteJSONValue `json:"headers" db:"headers"`
	Env                    sqliteJSONValue `json:"env" db:"env"`
	APIKey                 *string         `json:"api_key" db:"api_key"`
	ToolPrefix             *string         `json:"tool_prefix" db:"tool_prefix"`
	TimeoutSec             int             `json:"timeout_sec" db:"timeout_sec"`
	Settings               sqliteJSONValue `json:"settings" db:"settings"`
	Enabled                bool            `json:"enabled" db:"enabled"`
	RequireUserCredentials bool            `json:"require_user_credentials" db:"require_user_credentials"`
	CreatedBy              string          `json:"created_by" db:"created_by"`
	CreatedAt              sqliteTime      `json:"created_at" db:"created_at"`
	UpdatedAt              sqliteTime      `json:"updated_at" db:"updated_at"`
}

func (r *mcpServerRow) toMCPServerData() store.MCPServerData {
	return store.MCPServerData{
		BaseModel:              store.BaseModel{ID: r.ID, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time},
		Name:                   r.Name,
		DisplayName:            derefStr(r.DisplayName),
		Transport:              r.Transport,
		Command:                derefStr(r.Command),
		Args:                   json.RawMessage(r.Args),
		URL:                    derefStr(r.URL),
		Headers:                json.RawMessage(r.Headers),
		Env:                    json.RawMessage(r.Env),
		APIKey:                 derefStr(r.APIKey),
		ToolPrefix:             derefStr(r.ToolPrefix),
		TimeoutSec:             r.TimeoutSec,
		Settings:               json.RawMessage(r.Settings),
		Enabled:                r.Enabled,
		RequireUserCredentials: r.RequireUserCredentials,
		CreatedBy:              r.CreatedBy,
	}
}
