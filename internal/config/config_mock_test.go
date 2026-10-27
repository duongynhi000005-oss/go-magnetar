package config_test

import (
	"testing"

	"github.com/wmentor/go-magnetar/internal/config"
	"github.com/wmentor/go-magnetar/internal/config/testdata"
)

func TestMockConfig(t *testing.T) {
	t.Parallel()

	mockCfg := testdata.NewConfig(t)
	copyMock := testdata.NewConfig(t)

	mockCfg.EXPECT().Bool("guard.enable").Return(true)
	mockCfg.EXPECT().Int("ssh.timeout").Return(120)
	mockCfg.EXPECT().Float64("llm.temperature").Return(0.75)
	mockCfg.EXPECT().String("language").Return("english")
	mockCfg.EXPECT().Strings("guard.allow").Return([]string{"ls", "pwd"})
	mockCfg.EXPECT().Profiles().Return([]string{"fast", "main"})
	mockCfg.EXPECT().Profile().Return("main")
	mockCfg.EXPECT().SetProfile("fast").Return(nil)
	mockCfg.EXPECT().ProfileParamBool("rag.enable").Return(true)
	mockCfg.EXPECT().ProfileParamInt("rag.search.limit").Return(10)
	mockCfg.EXPECT().ProfileParamFloat64("rag.search.threshold").Return(0.4)
	mockCfg.EXPECT().ProfileParamString("llm.model").Return("gpt-4o")
	mockCfg.EXPECT().Copy().Return(copyMock)

	var cfg config.Config = mockCfg

	if !cfg.Bool("guard.enable") {
		t.Fatal("expected Bool to return true")
	}
	if got := cfg.Int("ssh.timeout"); got != 120 {
		t.Fatalf("Int = %d, want 120", got)
	}
	if got := cfg.Float64("llm.temperature"); got != 0.75 {
		t.Fatalf("Float64 = %v, want 0.75", got)
	}
	if got := cfg.String("language"); got != "english" {
		t.Fatalf("String = %q, want \"english\"", got)
	}
	if got := cfg.Strings("guard.allow"); len(got) != 2 || got[0] != "ls" || got[1] != "pwd" {
		t.Fatalf("Strings = %v, want [ls pwd]", got)
	}
	if got := cfg.Profiles(); len(got) != 2 || got[0] != "fast" || got[1] != "main" {
		t.Fatalf("Profiles = %v, want [fast main]", got)
	}
	if got := cfg.Profile(); got != "main" {
		t.Fatalf("Profile = %q, want \"main\"", got)
	}
	if err := cfg.SetProfile("fast"); err != nil {
		t.Fatalf("SetProfile error = %v, want nil", err)
	}
	if !cfg.ProfileParamBool("rag.enable") {
		t.Fatal("expected ProfileParamBool to return true")
	}
	if got := cfg.ProfileParamInt("rag.search.limit"); got != 10 {
		t.Fatalf("ProfileParamInt = %d, want 10", got)
	}
	if got := cfg.ProfileParamFloat64("rag.search.threshold"); got != 0.4 {
		t.Fatalf("ProfileParamFloat64 = %v, want 0.4", got)
	}
	if got := cfg.ProfileParamString("llm.model"); got != "gpt-4o" {
		t.Fatalf("ProfileParamString = %q, want \"gpt-4o\"", got)
	}
	if got := cfg.Copy(); got != copyMock {
		t.Fatalf("Copy = %v, want copyMock", got)
	}
}
