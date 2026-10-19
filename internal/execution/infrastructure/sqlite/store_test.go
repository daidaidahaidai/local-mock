package sqlite

import (
	"context"
	"fmt"
	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
	"sync"
	"testing"
)

func TestConcurrentOpenAndWrites(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				s, err := Open(dir)
				if err != nil {
					t.Error(err)
					return
				}
				id := fmt.Sprintf("%d-%d", worker, n)
				err = s.Insert(context.Background(), domain.Run{ID: id, Project: id, Scenario: "concurrent", State: domain.Starting})
				if err != nil {
					t.Error(err)
				}
				if _, err = s.List(context.Background()); err != nil {
					t.Error(err)
				}
				s.Close()
			}
		}(worker)
	}
	wg.Wait()
}

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
