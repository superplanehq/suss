package reconcile

import "testing"

func TestVersionSatisfiesComposerCommaAndConstraints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		constraint string
		version    string
		want       bool
	}{
		{constraint: ">=8.1,<8.3", version: "8.2", want: true},
		{constraint: ">=8.1,<8.3", version: "8.4", want: false},
		{constraint: ">=8.1,<8.4", version: "8.3", want: true},
		{constraint: ">=8.1,<8.4", version: "8.3.6", want: true},
		{constraint: ">=8.1,<8.4", version: "8.4", want: false},
		{constraint: ">=8.1, <8.4", version: "8.1.0", want: true},
		{constraint: ">=8.1, <8.4", version: "8.4.0", want: false},
	}
	for _, tt := range tests {
		got, known := versionSatisfies("php", tt.constraint, tt.version)
		if !known || got != tt.want {
			t.Fatalf("versionSatisfies(%q, %q) = (%t, %t), want (%t, true)", tt.constraint, tt.version, got, known, tt.want)
		}
	}
}

func TestVersionSatisfiesComposerTildeHyphenAndInequality(t *testing.T) {
	t.Parallel()

	tests := []struct {
		constraint string
		version    string
		want       bool
		known      bool
	}{
		{constraint: "~8.1", version: "8.2", want: true, known: true},
		{constraint: "~8.1", version: "8.1.0", want: true, known: true},
		{constraint: "~8.1", version: "9.0.0", want: false, known: true},
		{constraint: "~8.1.0", version: "8.2", want: false, known: true},
		{constraint: "8.1 - 8.3", version: "8.2", want: true, known: true},
		{constraint: "8.1 - 8.3", version: "8.4", want: false, known: true},
		{constraint: "!=8.1", version: "8.2", want: true, known: true},
		{constraint: "!=8.2", version: "8.2", want: false, known: true},
		{constraint: "!=8.1", version: "8.1.0", want: false, known: true},
		{constraint: "<>8.1", version: "8.1.0", want: false, known: true},
		{constraint: "!=8.1.*", version: "8.1.5", want: false, known: true},
		{constraint: "!=8.1.*", version: "8.2", want: true, known: true},
		{constraint: "!= 8.1.*", version: "8.1.0", want: false, known: true},
		{constraint: "^8.1 | ^8.3", version: "8.3", want: true, known: true},
		{constraint: "^8.1 | ^8.3", version: "8.2", want: true, known: true},
		{constraint: "^8.3 | ^7.4", version: "8.2", want: false, known: true},
		{constraint: "^8.1|^8.3", version: "8.3.2", want: true, known: true},
		{constraint: "=8.3.0", version: "8.3.0", want: true, known: true},
		{constraint: "=8.3.0", version: "8.2.0", want: false, known: true},
		{constraint: "==8.3.0", version: "8.3.0", want: true, known: true},
		{constraint: "= 8.3.0", version: "8.3.0", want: true, known: true},
		{constraint: "=8.3", version: "8.3.0", want: true, known: true},
		{constraint: "8.3", version: "8.3.0", want: true, known: true},
		{constraint: "8.3", version: "8.2.0", want: false, known: true},
		{constraint: ">=8.1@dev", version: "8.2", want: true, known: true},
		{constraint: ">=8.1@dev", version: "8.0", want: false, known: true},
	}
	for _, tt := range tests {
		got, known := versionSatisfies("php", tt.constraint, tt.version)
		if known != tt.known || got != tt.want {
			t.Fatalf("versionSatisfies(php, %q, %q) = (%t, %t), want (%t, %t)", tt.constraint, tt.version, got, known, tt.want, tt.known)
		}
	}

	got, known := versionSatisfies("node", "~8.1", "8.2")
	if !known || got {
		t.Fatalf("versionSatisfies(node, ~8.1, 8.2) = (%t, %t), want npm tilde false", got, known)
	}
}

func TestVersionSatisfiesElixirPessimisticConstraint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		constraint string
		version    string
		want       bool
	}{
		{constraint: "~> 1.18", version: "1.20.2", want: true},
		{constraint: "~> 1.18", version: "2.0.0", want: false},
		{constraint: "~> 1.18.2", version: "1.18.9", want: true},
		{constraint: "~> 1.18.2", version: "1.19.0", want: false},
	}
	for _, tt := range tests {
		got, known := versionSatisfies("elixir", tt.constraint, tt.version)
		if !known || got != tt.want {
			t.Fatalf("versionSatisfies(%q, %q) = (%t, %t), want (%t, true)", tt.constraint, tt.version, got, known, tt.want)
		}
	}
}

func TestVersionSatisfiesPEP440PythonConstraints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		constraint string
		version    string
		want       bool
	}{
		{constraint: ">=3.9,<4", version: "3.10", want: true},
		{constraint: ">=3.9,<4", version: "3.9", want: true},
		{constraint: ">=3.9,<4", version: "4.0", want: false},
		{constraint: ">=3.9,<4", version: "4", want: false},
		{constraint: ">=3.9, <4", version: "3.12", want: true},
		{constraint: "~=3.11", version: "3.11", want: true},
		{constraint: "~=3.11", version: "3.12", want: true},
		{constraint: "~=3.11", version: "4.0", want: false},
		{constraint: "~=3.11.0", version: "3.11.5", want: true},
		{constraint: "~=3.11.0", version: "3.12", want: false},
	}
	for _, tt := range tests {
		got, known := versionSatisfies(tt.constraint, tt.version)
		if !known || got != tt.want {
			t.Fatalf("versionSatisfies(%q, %q) = (%t, %t), want (%t, true)", tt.constraint, tt.version, got, known, tt.want)
		}
	}
}

func TestVersionSatisfiesPrereleaseBoundsAreUnevaluable(t *testing.T) {
	t.Parallel()

	got, known := versionSatisfies(">=3.13rc1", "3.12")
	if known {
		t.Fatalf("versionSatisfies(>=3.13rc1, 3.12) = (%t, %t), want unevaluable", got, known)
	}
	got, known = versionSatisfies(">=3.13.0rc1", "3.13")
	if known {
		t.Fatalf("versionSatisfies(>=3.13.0rc1, 3.13) = (%t, %t), want unevaluable", got, known)
	}
}
