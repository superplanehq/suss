package schema_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/superplanehq/suss/schema"
)

func TestExampleDocumentsMatchSchema(t *testing.T) {
	t.Parallel()

	paths, err := filepath.Glob("examples/*.json")
	if err != nil {
		t.Fatalf("filepath.Glob() error = %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no contract examples found")
	}

	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("os.ReadFile() error = %v", err)
			}
			if err := schema.Validate(contents); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	err := schema.Validate([]byte(`{"schemaVersion":"1","projects":[],"extra":true}`))
	if err == nil {
		t.Fatal("Validate() error = nil, want an additional-properties error")
	}
}
