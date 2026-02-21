package plugin

import (
	"context"
	"testing"

	"github.com/allaspects/tokenman/internal/pipeline"
)

// testPlugin is a minimal Plugin for testing.
type testPlugin struct {
	name    string
	version string
	closed  bool
}

func (p *testPlugin) Name() string                        { return p.name }
func (p *testPlugin) Version() string                     { return p.version }
func (p *testPlugin) Init(map[string]interface{}) error    { return nil }
func (p *testPlugin) Close() error                        { p.closed = true; return nil }

// testMiddlewarePlugin implements Plugin + MiddlewarePlugin.
type testMiddlewarePlugin struct {
	testPlugin
}

func (p *testMiddlewarePlugin) Enabled() bool { return true }
func (p *testMiddlewarePlugin) ProcessRequest(_ context.Context, req *pipeline.Request) (*pipeline.Request, error) {
	return req, nil
}
func (p *testMiddlewarePlugin) ProcessResponse(_ context.Context, req *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error) {
	return resp, nil
}

func TestRegistry_Register_List(t *testing.T) {
	r := NewRegistry()

	p1 := &testPlugin{name: "test-a", version: "1.0"}
	p2 := &testPlugin{name: "test-b", version: "2.0"}

	if err := r.Register(p1, nil); err != nil {
		t.Fatalf("Register p1: %v", err)
	}
	if err := r.Register(p2, nil); err != nil {
		t.Fatalf("Register p2: %v", err)
	}

	infos := r.List()
	if len(infos) != 2 {
		t.Fatalf("List: got %d plugins, want 2", len(infos))
	}
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	r := NewRegistry()

	p := &testPlugin{name: "dup", version: "1.0"}
	if err := r.Register(p, nil); err != nil {
		t.Fatalf("first Register: %v", err)
	}

	p2 := &testPlugin{name: "dup", version: "1.0"}
	err := r.Register(p2, nil)
	if err == nil {
		t.Fatal("expected error registering duplicate plugin")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()

	p := &testPlugin{name: "removable", version: "1.0"}
	if err := r.Register(p, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := r.Unregister("removable"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	if !p.closed {
		t.Error("Close was not called on unregistered plugin")
	}

	infos := r.List()
	if len(infos) != 0 {
		t.Errorf("List after Unregister: got %d, want 0", len(infos))
	}
}

func TestRegistry_UnregisterNotFound(t *testing.T) {
	r := NewRegistry()

	err := r.Unregister("nonexistent")
	if err == nil {
		t.Fatal("expected error unregistering nonexistent plugin")
	}
}

func TestRegistry_MiddlewarePluginCategorization(t *testing.T) {
	r := NewRegistry()

	mp := &testMiddlewarePlugin{testPlugin: testPlugin{name: "mw-plugin", version: "1.0"}}
	if err := r.Register(mp, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mws := r.Middleware()
	if len(mws) != 1 {
		t.Fatalf("Middleware: got %d, want 1", len(mws))
	}
	if mws[0].Name() != "mw-plugin" {
		t.Errorf("middleware name: got %q, want %q", mws[0].Name(), "mw-plugin")
	}

	// Non-middleware lists should be empty.
	if len(r.Transforms()) != 0 {
		t.Errorf("Transforms: got %d, want 0", len(r.Transforms()))
	}
	if len(r.Hooks()) != 0 {
		t.Errorf("Hooks: got %d, want 0", len(r.Hooks()))
	}
}

func TestRegistry_CloseAll(t *testing.T) {
	r := NewRegistry()

	p1 := &testPlugin{name: "a", version: "1.0"}
	p2 := &testPlugin{name: "b", version: "1.0"}
	_ = r.Register(p1, nil)
	_ = r.Register(p2, nil)

	r.CloseAll()

	if !p1.closed || !p2.closed {
		t.Error("not all plugins were closed")
	}

	if len(r.List()) != 0 {
		t.Error("registry not empty after CloseAll")
	}
}

func TestRegistry_UnregisterRemovesMiddleware(t *testing.T) {
	r := NewRegistry()

	mp := &testMiddlewarePlugin{testPlugin: testPlugin{name: "mw-rm", version: "1.0"}}
	_ = r.Register(mp, nil)

	if len(r.Middleware()) != 1 {
		t.Fatal("middleware not registered")
	}

	_ = r.Unregister("mw-rm")

	if len(r.Middleware()) != 0 {
		t.Error("middleware not removed after Unregister")
	}
}
