package smtpplugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wmentor/go-magnetar/internal/config"
	"github.com/wmentor/go-magnetar/internal/plugin"
	"github.com/wmentor/go-magnetar/internal/plugin/testdata"
)

func TestSMTPPluginDisabledByDefault(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	hub := testdata.NewHub(t)
	p := &Plugin{}
	if err := p.Init(&plugin.State{Config: cfg}, hub); err != nil {
		t.Fatalf("Init unexpected error: %v", err)
	}
	if p.IsEnabled() {
		t.Error("expected plugin to be disabled when smtp.enable is false")
	}
}
