//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// TestSQLiteSkillGrantsReturnErrSkillNotFound pins the store-layer contract the
// HTTP grant/revoke/list handlers rely on: a well-formed but non-existent skill
// id must be distinguishable from a genuine database error. Before this, every
// grant/revoke/list call returned a bare fmt.Errorf("skill not found") that
// errors.Is could never match, so the handler always fell through to its
// generic 500 branch for a missing skill instead of 404.
func TestSQLiteSkillGrantsReturnErrSkillNotFound(t *testing.T) {
	ctx, skillStore := newTestSQLiteSkillStore(t)
	missingSkill := uuid.New()
	agentID := uuid.New()

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"ListAgentGrantsForSkill", func() error {
			_, err := skillStore.ListAgentGrantsForSkill(ctx, missingSkill)
			return err
		}},
		{"ListUserGrantsForSkill", func() error {
			_, err := skillStore.ListUserGrantsForSkill(ctx, missingSkill)
			return err
		}},
		{"GrantToAgent", func() error {
			return skillStore.GrantToAgent(ctx, missingSkill, agentID, 1, "user-1")
		}},
		{"RevokeFromAgent", func() error {
			return skillStore.RevokeFromAgent(ctx, missingSkill, agentID)
		}},
		{"GrantToUser", func() error {
			return skillStore.GrantToUser(ctx, missingSkill, "user-1", "user-1")
		}},
		{"RevokeFromUser", func() error {
			return skillStore.RevokeFromUser(ctx, missingSkill, "user-1")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("want an error for a non-existent skill, got nil")
			}
			if !errors.Is(err, store.ErrSkillNotFound) {
				t.Errorf("error = %q, want errors.Is(err, store.ErrSkillNotFound)", err)
			}
		})
	}
}

// TestSQLiteSkillGrantsCrossTenantReturnsErrSkillNotFound covers the second
// verifySkillInGrantScope branch: a skill that exists but belongs to a
// different tenant must also map to ErrSkillNotFound, not leak its existence.
func TestSQLiteSkillGrantsCrossTenantReturnsErrSkillNotFound(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "skills.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	skillStore := NewSQLiteSkillStore(db, t.TempDir())

	otherTenant := uuid.New()
	if _, err := db.Exec(
		`INSERT INTO tenants (id, name, slug, status) VALUES (?, ?, ?, 'active')`,
		otherTenant.String(), "tenant-"+otherTenant.String()[:8], "t"+otherTenant.String()[:8],
	); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	ownerCtx := store.WithTenantID(context.Background(), otherTenant)
	skillID, err := skillStore.CreateSkillManaged(ownerCtx, store.SkillCreateParams{
		Name: "Other Tenant Skill", Slug: "other-tenant-skill", OwnerID: "user-1",
		Visibility: "private", FilePath: filepath.Join(t.TempDir(), "other-tenant-skill", "1"),
	})
	if err != nil {
		t.Fatalf("CreateSkillManaged: %v", err)
	}

	callerCtx := store.WithTenantID(context.Background(), store.MasterTenantID)
	if _, err := skillStore.ListAgentGrantsForSkill(callerCtx, skillID); !errors.Is(err, store.ErrSkillNotFound) {
		t.Errorf("cross-tenant list error = %v, want errors.Is(err, store.ErrSkillNotFound)", err)
	}
}
