package logging

import "testing"

func TestNop_DoesNotPanic(t *testing.T) {
	Nop("anything %d %s", 1, "two")
}

func TestOrNop_ReturnsProvidedLogger(t *testing.T) {
	var got string
	logf := OrNop(func(format string, args ...any) { got = format })
	logf("hello")

	if got != "hello" {
		t.Fatalf("expected the supplied logger to be used, got %q", got)
	}
}

func TestOrNop_SubstitutesNop(t *testing.T) {
	logf := OrNop(nil)
	if logf == nil {
		t.Fatal("OrNop(nil) must return a usable logger")
	}
	logf("must not panic %v", 1)
}
