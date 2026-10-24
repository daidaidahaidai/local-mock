package docker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
)

func TestInitializationUsesVerifiedSnapshot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "fixture.sql")
	data := []byte("CREATE TABLE test (id INT);\n")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	r := domain.Run{Directory: t.TempDir()}
	if err := (Driver{}).Prepare(context.Background(), r, domain.Spec{Inputs: map[string]string{file: hash}}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(file, []byte("changed after snapshot"), 0600)
	got, err := initializationInput(r, hash)
	if err != nil || string(got) != string(data) {
		t.Fatalf("snapshot: %s %v", got, err)
	}
	os.WriteFile(filepath.Join(r.Directory, "inputs", hash), []byte("tampered"), 0600)
	if _, err = initializationInput(r, hash); err == nil {
		t.Fatal("tampered snapshot accepted")
	}
	if _, err = initializationInput(r, "../../input"); err == nil {
		t.Fatal("non-hash accepted")
	}
}

func TestInitializationOutputLimitDuringCopy(t *testing.T) {
	b := &boundedOutput{limit: 10}
	n, err := io.Copy(b, io.LimitReader(strings.NewReader(strings.Repeat("x", 100)), 100))
	if err != nil || n != 100 || len(b.String()) != 10 || !b.truncated {
		t.Fatalf("unbounded output: copied=%d stored=%d error=%v", n, len(b.String()), err)
	}
}
