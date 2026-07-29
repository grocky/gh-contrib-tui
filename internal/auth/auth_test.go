package auth

import "testing"

func TestDedupe(t *testing.T) {
	got := Dedupe([]Candidate{
		{Source: "A", Token: "same"},
		{Source: "B", Token: "same"},
		{Source: "C", Token: "other"},
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(got))
	}
	if got[0].Source != "A" || got[1].Source != "C" {
		t.Fatalf("expected stable first-seen order, got %+v", got)
	}
}

func TestDedupe_Empty(t *testing.T) {
	if got := Dedupe(nil); len(got) != 0 {
		t.Fatalf("expected empty result, got %+v", got)
	}
}

func TestCandidateFingerprint(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{name: "long token is masked", token: "ghp_abcdefghijklmnop", want: "ghp_...mnop(len:20)"},
		{name: "short token is length only", token: "abc", want: "len:3"},
		{name: "boundary token is length only", token: "12345678", want: "len:8"},
		{name: "empty token", token: "", want: "len:0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Candidate{Token: tc.token}.Fingerprint()
			if got != tc.want {
				t.Fatalf("Fingerprint(%q) = %q, want %q", tc.token, got, tc.want)
			}
		})
	}
}

func TestCandidateFingerprint_DoesNotLeakToken(t *testing.T) {
	token := "ghp_supersecrettokenvalue"
	if got := (Candidate{Token: token}).Fingerprint(); len(got) >= len(token) {
		t.Fatalf("fingerprint %q looks unmasked", got)
	}
}

func TestResolveCandidates_EnvOrderAndDedupe(t *testing.T) {
	t.Setenv("PATH", "") // keep the gh CLI out of the results
	t.Setenv("GITHUB_TOKEN", "token-one")
	t.Setenv("GH_TOKEN", "token-two")

	got, err := ResolveCandidates()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %+v", got)
	}
	if got[0].Source != "GITHUB_TOKEN" || got[0].Token != "token-one" {
		t.Fatalf("expected GITHUB_TOKEN first, got %+v", got[0])
	}
	if got[1].Source != "GH_TOKEN" || got[1].Token != "token-two" {
		t.Fatalf("expected GH_TOKEN second, got %+v", got[1])
	}

	t.Setenv("GH_TOKEN", "token-one")
	got, err = ResolveCandidates()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Source != "GITHUB_TOKEN" {
		t.Fatalf("expected duplicate token values to collapse, got %+v", got)
	}
}

func TestResolveCandidates_TrimsWhitespace(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_TOKEN", "  spaced-token\n")
	t.Setenv("GH_TOKEN", "   ")

	got, err := ResolveCandidates()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Token != "spaced-token" {
		t.Fatalf("expected whitespace-only vars ignored and tokens trimmed, got %+v", got)
	}
}

func TestResolveCandidates_NoTokens(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	if _, err := ResolveCandidates(); err == nil {
		t.Fatal("expected an error when no token source is available")
	}
}
