package lock

import "testing"

func TestExclusiveAndReleased(t *testing.T) {
	l := Locker{Root: t.TempDir()}
	id := "012345678901234567890123"
	release, err := l.Acquire(id)
	if err != nil {
		t.Fatal(err)
	}
	if unlock, err := l.Acquire(id); err == nil {
		unlock()
		t.Fatal("accepted concurrent operation")
	}
	release()
	unlock, err := l.Acquire(id)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if _, err = l.Acquire("../../other"); err == nil {
		t.Fatal("accepted invalid ID")
	}
}
