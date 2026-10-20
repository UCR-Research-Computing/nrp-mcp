package ops

import (
	"strings"
	"testing"
)

func TestCleanStripsEscapesAndCaps(t *testing.T) {
	in := "ok\x1b[31mRED\x1b[0m\x07 bell\u202eevil\nnext\tline"
	got := Clean(in, 0)
	if got != "okRED bellevil\nnext\tline" {
		t.Fatalf("%q", got)
	}
	long := strings.Repeat("a", 100) + "END"
	if got := Clean(long, 10); !strings.HasSuffix(got, "aaaaaaaEND") || !strings.HasPrefix(got, "[... earlier") {
		t.Fatalf("%q", got)
	}
}
