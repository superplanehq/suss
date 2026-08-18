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
		got, known := versionSatisfies(tt.constraint, tt.version)
		if !known || got != tt.want {
			t.Fatalf("versionSatisfies(%q, %q) = (%t, %t), want (%t, true)", tt.constraint, tt.version, got, known, tt.want)
		}
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
		got, known := versionSatisfies(tt.constraint, tt.version)
		if !known || got != tt.want {
			t.Fatalf("versionSatisfies(%q, %q) = (%t, %t), want (%t, true)", tt.constraint, tt.version, got, known, tt.want)
		}
	}
}
