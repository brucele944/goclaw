package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

func TestHandleConnectRejectsNoTokenExternalBind(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Host = "0.0.0.0"
	cfg.Gateway.Token = ""
	t.Setenv(config.GatewayAllowInsecureNoAuthEnv, "")

	server := NewServer(cfg, nil, nil, nil)
	client := NewClient(nil, server, "203.0.113.10")
	req := &protocol.RequestFrame{ID: "req-1", Method: protocol.MethodConnect}

	server.router.Handle(context.Background(), client, req)

	if client.authenticated {
		t.Fatal("expected unauthenticated client for external no-token connect")
	}
	if client.role != "" {
		t.Fatalf("role = %q, want empty", client.role)
	}
	select {
	case raw := <-client.send:
		var resp protocol.ResponseFrame
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if resp.Error == nil || resp.Error.Code != protocol.ErrUnauthorized {
			t.Fatalf("response error = %#v, want unauthorized", resp.Error)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected unauthorized response")
	}
}

func TestHandleConnectAllowsExplicitInsecureNoTokenOptIn(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Host = "0.0.0.0"
	cfg.Gateway.Token = ""
	t.Setenv(config.GatewayAllowInsecureNoAuthEnv, "1")

	server := NewServer(cfg, nil, nil, nil)
	client := NewClient(nil, server, "127.0.0.1")
	req := &protocol.RequestFrame{ID: "req-1", Method: protocol.MethodConnect}

	server.router.Handle(context.Background(), client, req)

	if !client.authenticated {
		t.Fatal("expected authenticated client with explicit insecure opt-in")
	}
	if client.role != permissions.RoleOperator {
		t.Fatalf("role = %q, want operator", client.role)
	}
}

// callMethod dispatches method through the router and returns the response frame.
func callMethod(t *testing.T, server *Server, client *Client, method string) *protocol.ResponseFrame {
	t.Helper()
	req := &protocol.RequestFrame{ID: "req-" + method, Method: method}
	server.router.Handle(context.Background(), client, req)
	select {
	case raw := <-client.send:
		var resp protocol.ResponseFrame
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		return &resp
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected a response frame for %s", method)
		return nil
	}
}

// TestRouterProvisionScopeTenantMethods covers the narrow method-scoped
// exception for operator.provision credentials (issue #1524): provision-only
// API keys are admitted on exactly tenants.create and tenants.users.add —
// the two methods the tenant handlers already gate on ScopeProvision — while
// every other admin/write surface stays denied and plain viewers gain nothing.
func TestRouterProvisionScopeTenantMethods(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Token = "test-token"

	server := NewServer(cfg, nil, nil, nil)
	server.SetPolicyEngine(permissions.NewPolicyEngine(nil))

	reached := map[string]bool{}
	for _, method := range []string{
		protocol.MethodTenantsCreate,
		protocol.MethodTenantsUsersAdd,
		protocol.MethodTenantsUpdate,
		protocol.MethodAgentsCreate,
	} {
		m := method
		server.router.Register(m, func(ctx context.Context, c *Client, req *protocol.RequestFrame) {
			reached[m] = true
			c.SendResponse(protocol.NewOKResponse(req.ID, map[string]any{"ok": true}))
		})
	}

	// Provision-only API key: RoleFromScopes maps it to viewer; scopes carry
	// operator.provision (as the WS connect path would set them).
	prov, _ := NewCapturingTestClient(permissions.RoleViewer, uuid.Nil, "provisioner", 8)
	prov.scopes = []permissions.Scope{permissions.ScopeProvision}

	// 1) Provision-only succeeds on the two tenant-provisioning methods.
	for _, method := range []string{protocol.MethodTenantsCreate, protocol.MethodTenantsUsersAdd} {
		resp := callMethod(t, server, prov, method)
		if resp.Error != nil {
			t.Fatalf("provision-only %s: unexpected error %v (code %s)", method, resp.Error.Message, resp.Error.Code)
		}
		if !reached[method] {
			t.Fatalf("provision-only %s: handler never invoked", method)
		}
	}

	// 2) Provision-only remains denied on other admin/write surfaces.
	for _, method := range []string{protocol.MethodTenantsUpdate, protocol.MethodAgentsCreate} {
		resp := callMethod(t, server, prov, method)
		if resp.Error == nil || resp.Error.Code != protocol.ErrUnauthorized {
			t.Fatalf("provision-only %s: want unauthorized, got error=%v reached=%v", method, resp.Error, reached[method])
		}
	}

	// 3) A viewer WITHOUT the provision scope still cannot reach the two
	// tenant-provisioning methods.
	viewer, _ := NewCapturingTestClient(permissions.RoleViewer, uuid.Nil, "viewer", 8)
	for _, method := range []string{protocol.MethodTenantsCreate, protocol.MethodTenantsUsersAdd} {
		resp := callMethod(t, server, viewer, method)
		if resp.Error == nil || resp.Error.Code != protocol.ErrUnauthorized {
			t.Fatalf("plain viewer %s: want unauthorized, got error=%v reached=%v", method, resp.Error, reached[method])
		}
	}

	// 4) An unauthenticated client stays denied.
	anon, _ := NewCapturingTestClient("", uuid.Nil, "", 8)
	resp := callMethod(t, server, anon, protocol.MethodTenantsCreate)
	if resp.Error == nil || resp.Error.Code != protocol.ErrUnauthorized {
		t.Fatalf("unauthenticated tenants.create: want unauthorized, got error=%v reached=%v", resp.Error, reached[protocol.MethodTenantsCreate])
	}
}
