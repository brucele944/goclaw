//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// TestSQLiteTenantStoreRoundTripsJSONColumns pins the scan path for the tenants
// table: its JSON columns are TEXT, and a scan target without a Scan method
// (json.RawMessage) cannot absorb the driver's string value, which broke
// ListTenants/GetTenant on every SQLite deployment.
func TestSQLiteTenantStoreRoundTripsJSONColumns(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	initSqlx(db)

	s := NewSQLiteTenantStore(db)
	ctx := context.Background()
	settings := json.RawMessage(`{"locale":"vi","feature":{"x":1}}`)

	tenant := &store.TenantData{
		ID:       store.GenNewID(),
		Name:     "Acme",
		Slug:     "acme",
		Status:   "active",
		Settings: settings,
	}
	if err := s.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	got, err := s.GetTenant(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	if string(got.Settings) != string(settings) {
		t.Errorf("GetTenant settings = %s, want %s", got.Settings, settings)
	}

	bySlug, err := s.GetTenantBySlug(ctx, "acme")
	if err != nil {
		t.Fatalf("GetTenantBySlug: %v", err)
	}
	if string(bySlug.Settings) != string(settings) {
		t.Errorf("GetTenantBySlug settings = %s, want %s", bySlug.Settings, settings)
	}

	list, err := s.ListTenants(ctx)
	if err != nil {
		t.Fatalf("ListTenants: %v", err)
	}
	var found *store.TenantData
	for i := range list {
		if list[i].ID == tenant.ID {
			found = &list[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("ListTenants did not return the created tenant (%d rows)", len(list))
	}
	if string(found.Settings) != string(settings) {
		t.Errorf("ListTenants settings = %s, want %s", found.Settings, settings)
	}
	if found.Name != "Acme" || found.Slug != "acme" || found.Status != "active" {
		t.Errorf("ListTenants row = %+v, want the created tenant's fields", found)
	}
}

// TestSQLiteTenantUserStoreRoundTripsMetadata covers the same scan contract for
// tenant_users.metadata, which is read by the membership lookups.
func TestSQLiteTenantUserStoreRoundTripsMetadata(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	initSqlx(db)

	s := NewSQLiteTenantStore(db)
	ctx := context.Background()
	tenantID := store.GenNewID()
	if err := s.CreateTenant(ctx, &store.TenantData{
		ID: tenantID, Name: "Acme", Slug: "acme", Status: "active",
		Settings: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	metadata := json.RawMessage(`{"invited_by":"admin"}`)
	user, err := s.CreateTenantUserReturning(ctx, tenantID, "user-1", "User One", "admin")
	if err != nil {
		t.Fatalf("CreateTenantUserReturning: %v", err)
	}
	if user == nil {
		t.Fatal("CreateTenantUserReturning returned nil")
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE tenant_users SET metadata = ? WHERE tenant_id = ? AND user_id = ?`,
		string(metadata), tenantID, "user-1"); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}

	users, err := s.ListUsers(ctx, tenantID)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("ListUsers returned %d rows, want 1", len(users))
	}
	if string(users[0].Metadata) != string(metadata) {
		t.Errorf("ListUsers metadata = %s, want %s", users[0].Metadata, metadata)
	}

	role, err := s.GetUserRole(ctx, tenantID, "user-1")
	if err != nil {
		t.Fatalf("GetUserRole: %v", err)
	}
	if role != "admin" {
		t.Errorf("GetUserRole = %q, want admin", role)
	}
}
