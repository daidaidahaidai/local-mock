package repository

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestToolsCannotReadSecretsOrEscapeWorkspace(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte("secret"), 0600)
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(dir, "link"))
	os.Symlink(filepath.Join(dir, ".env"), filepath.Join(dir, "alias"))
	w, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	tool := w.Tools()[1]
	for _, path := range []string{".env", "../outside", "link", "alias", outside} {
		args, _ := json.Marshal(map[string]string{"path": path})
		if _, err = tool.Call(context.Background(), args); err == nil {
			t.Errorf("read forbidden path %s", path)
		}
	}
}
func TestEvidenceMustHaveBeenRead(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0600)
	w, _ := New(dir)
	report := json.RawMessage(`{"services":[{"name":"app","language":"Go","evidence":["main.go"]}],"unknowns":[]}`)
	if err := w.Validate(report); err == nil {
		t.Fatal("accepted unread evidence")
	}
	if _, err := w.Tools()[1].Call(context.Background(), json.RawMessage(`{"path":"main.go"}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Validate(report); err != nil {
		t.Fatal(err)
	}
}
