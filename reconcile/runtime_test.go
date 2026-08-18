package reconcile

import "testing"

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
