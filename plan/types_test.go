package plan

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestNewCommandIDUsesStableSourceIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		identity CommandIdentity
		want     CommandID
	}{
		{
			name: "declared package script",
			identity: CommandIdentity{
				ProjectPath: "frontend",
				Provider:    "node",
				Source:      "frontend/package.json",
				Pointer:     "/scripts/test",
			},
			want: "cmd_dd1307f3ba9312f568756953bd9994fc",
		},
		{
			name: "inferred Go convention",
			identity: CommandIdentity{
				ProjectPath: "backend",
				Provider:    "go",
				Source:      "go-ecosystem",
				Pointer:     "test",
			},
			want: "cmd_5a2e00071e9289d2c2010509f171ef8e",
		},
		{
			name: "observed CI preparation",
			identity: CommandIdentity{
				ProjectPath: "frontend",
				Provider:    "github-actions",
				Source:      ".github/workflows/ci.yml",
				Pointer:     "/jobs/frontend/steps/2/run#command=0",
			},
			want: "cmd_13d7ed10fffef5f7010cbbaabd7943ca",
		},
		{
			name: "root package script",
			identity: CommandIdentity{
				ProjectPath: ".",
				Provider:    "node",
				Source:      "package.json",
				Pointer:     "/scripts/build",
			},
			want: "cmd_ac1d00ae28290dacfd227b74a3abb62d",
		},
		{
			name: "inferred root package install",
			identity: CommandIdentity{
				ProjectPath: ".",
				Provider:    "node",
				Source:      "package.json",
				Pointer:     "/packageManager#install",
			},
			want: "cmd_ae21e52c391ccef56f7107de041061bb",
		},
		{
			name: "unlinked repository CI command",
			identity: CommandIdentity{
				ProjectPath: ".",
				Provider:    "github-actions",
				Source:      ".github/workflows/ci.yml",
				Pointer:     "/jobs/repository/steps/3/run",
			},
			want: "cmd_4eb5db128c5b8a9cc7993c351fb49229",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewCommandID(tt.identity)
			if err != nil {
				t.Fatalf("NewCommandID() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("NewCommandID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewCommandIDRejectsIncompleteIdentity(t *testing.T) {
	t.Parallel()

	_, err := NewCommandID(CommandIdentity{
		ProjectPath: ".",
		Provider:    "node",
		Source:      "package.json",
	})
	if err == nil {
		t.Fatal("NewCommandID() error = nil, want an incomplete identity error")
	}
}

func TestNewDocumentInitializesRequiredCollections(t *testing.T) {
	t.Parallel()

	document := NewDocument(nil)
	document.Projects = append(document.Projects, NewProjectPlan("."))

	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var decoded Document
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if decoded.Projects == nil {
		t.Fatal("projects decoded as null")
	}
	assertEmptyCollections(t, decoded.Projects[0])
}

func TestExamplesDecodeIntoContractTypes(t *testing.T) {
	t.Parallel()

	for _, examplePath := range examplePaths(t) {
		examplePath := examplePath
		t.Run(filepath.Base(examplePath), func(t *testing.T) {
			t.Parallel()

			file, err := os.Open(examplePath)
			if err != nil {
				t.Fatalf("os.Open() error = %v", err)
			}
			defer func() {
				if err := file.Close(); err != nil {
					t.Errorf("file.Close() error = %v", err)
				}
			}()

			decoder := json.NewDecoder(file)
			decoder.DisallowUnknownFields()

			var document Document
			if err := decoder.Decode(&document); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if err := decoder.Decode(&struct{}{}); err != io.EOF {
				t.Fatalf("Decode() trailing content error = %v, want io.EOF", err)
			}
			if document.SchemaVersion != SchemaVersion {
				t.Fatalf("schemaVersion = %q, want %q", document.SchemaVersion, SchemaVersion)
			}
			if err := document.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestExamplesMatchCanonicalEncoding(t *testing.T) {
	t.Parallel()

	for _, examplePath := range examplePaths(t) {
		examplePath := examplePath
		t.Run(filepath.Base(examplePath), func(t *testing.T) {
			t.Parallel()

			contents, err := os.ReadFile(examplePath)
			if err != nil {
				t.Fatalf("os.ReadFile() error = %v", err)
			}

			var document Document
			if err := json.Unmarshal(contents, &document); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			got, err := document.MarshalCanonical()
			if err != nil {
				t.Fatalf("MarshalCanonical() error = %v", err)
			}
			if string(got) != string(contents) {
				t.Fatalf("example is not in canonical form\n got:\n%s\nwant:\n%s", got, contents)
			}
		})
	}
}

func TestAmbiguousDeclaredTaskRemainsAddressable(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile("../schema/examples/competing-lockfiles.json")
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}

	var document Document
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	command := document.Projects[0].Commands[0]
	if command.Run != nil {
		t.Fatalf("command.Run = %q, want an unresolved invocation", *command.Run)
	}

	ambiguity := document.Projects[0].Ambiguities[0]
	if ambiguity.CommandID == nil {
		t.Fatal("ambiguity.CommandID = nil, want a command reference")
	}
	if *ambiguity.CommandID != command.ID {
		t.Fatalf("ambiguity.CommandID = %q, want %q", *ambiguity.CommandID, command.ID)
	}
}

func examplePaths(t *testing.T) []string {
	t.Helper()

	paths, err := filepath.Glob("../schema/examples/*.json")
	if err != nil {
		t.Fatalf("filepath.Glob() error = %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no contract examples found")
	}
	return paths
}

func assertEmptyCollections(t *testing.T, project ProjectPlan) {
	t.Helper()

	if project.Languages == nil {
		t.Fatal("languages decoded as null")
	}
	if project.Frameworks == nil {
		t.Fatal("frameworks decoded as null")
	}
	if project.PackageManagers == nil {
		t.Fatal("packageManagers decoded as null")
	}
	if project.Facts == nil {
		t.Fatal("facts decoded as null")
	}
	if project.Requirements == nil {
		t.Fatal("requirements decoded as null")
	}
	if project.Preparation == nil {
		t.Fatal("preparation decoded as null")
	}
	if project.Commands == nil {
		t.Fatal("commands decoded as null")
	}
	if project.Ambiguities == nil {
		t.Fatal("ambiguities decoded as null")
	}
	if project.Conflicts == nil {
		t.Fatal("conflicts decoded as null")
	}
}
