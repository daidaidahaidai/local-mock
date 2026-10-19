package sqlite

import (
	"context"
	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
	"testing"
)

func TestRunSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := domain.Run{ID: "one", Project: "localmock-one", Scenario: "test", State: domain.Starting, Directory: dir, CreatedAt: "2026-01-01"}
	if err = s.Insert(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	r.State = domain.Ready
	if err = s.Save(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Get(context.Background(), "one")
	if err != nil || got.State != domain.Ready {
		t.Fatalf("%+v %v", got, err)
	}
	if err = s.Insert(context.Background(), r); err == nil {
		t.Fatal("accepted duplicate run")
	}
}
