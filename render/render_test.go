package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/superplanehq/suss/plan"
)

func TestWriteReportsNoProjectRoots(t *testing.T) {
	t.Parallel()

	got := renderDocument(plan.NewDocument(nil), []string{"node"})
	if !strings.Contains(got, "No project roots were detected") {
		t.Fatalf("output %q, want an empty-repository explanation", got)
	}
	if strings.Contains(got, "Providers:") {
		t.Fatalf("output %q, want no supported-provider catalog", got)
	}
}

func TestWriteExplainsUnclaimedProject(t *testing.T) {
	t.Parallel()

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{plan.NewProjectPlan(".")}), []string{"node", "go"})
	if !strings.Contains(got, "No implemented provider produced findings for this project") {
		t.Fatalf("output %q, want an unclaimed-project explanation", got)
	}
	if !strings.Contains(got, "Providers that ran: node, go") {
		t.Fatalf("output %q, want the providers that ran", got)
	}
	if !strings.Contains(got, "Repository root\n===============") {
		t.Fatalf("output %q, want a human heading for the repository root", got)
	}
	if strings.Contains(got, "Project: .") {
		t.Fatalf("output %q, want no JSON-path project heading", got)
	}
}

func TestWriteOmitsHighConfidenceFixtureProjects(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Languages = []plan.DetectedValue{{Name: "go"}}

	fixture := plan.NewProjectPlan("testdata/sample")
	fixture.Facts = []plan.ProjectFact{{
		Name:       "project.role",
		Value:      "fixture",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceFile, Source: "testdata/sample"}},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{root, fixture}), []string{"node"})
	if !strings.Contains(got, "1 fixture project omitted; use --json to inspect") {
		t.Fatalf("output %q, want an omitted-fixture notice", got)
	}
	if strings.Contains(got, "Project: testdata/sample") {
		t.Fatalf("output %q, want the fixture project omitted", got)
	}
	if !strings.Contains(got, "Repository root") {
		t.Fatalf("output %q, want the primary project", got)
	}
	if strings.Contains(got, "Project: .") {
		t.Fatalf("output %q, want no JSON-path project heading", got)
	}
}

func TestWriteRendersStandaloneExampleProjectsInFull(t *testing.T) {
	t.Parallel()

	project := plan.NewProjectPlan("examples/demo")
	project.Facts = []plan.ProjectFact{{
		Name:       "project.role",
		Value:      "fixture",
		Confidence: plan.ConfidenceMedium,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceFile, Source: "examples/demo"}},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"node"})
	if strings.Contains(got, "fixture project omitted") {
		t.Fatalf("output %q, want no omitted project notice", got)
	}
	if !strings.Contains(got, "Example: examples/demo") {
		t.Fatalf("output %q, want the example labeled as an example", got)
	}
	if strings.Contains(got, "Project: examples/demo") {
		t.Fatalf("output %q, want no peer-project heading for an example", got)
	}
	if !strings.Contains(got, "project.role: fixture") {
		t.Fatalf("output %q, want the fixture fact", got)
	}
}

func TestWriteIndexesExampleProjectsAfterPrimary(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Languages = []plan.DetectedValue{{Name: "elixir"}}
	install := "mix deps.get"
	root.Preparation = []plan.Command{{
		Run: &install,
		Interpretations: []plan.Interpretation{{
			Capability: plan.CapabilityDependenciesInstall,
		}},
	}}

	example := plan.NewProjectPlan("examples/friends")
	example.Languages = []plan.DetectedValue{{Name: "elixir"}}
	example.PackageManagers = []plan.DetectedTool{{Name: "mix"}}
	example.Facts = []plan.ProjectFact{{
		Name:       "project.role",
		Value:      "fixture",
		Confidence: plan.ConfidenceMedium,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceFile, Source: "examples/friends"}},
	}}
	exampleInstall := "mix deps.get"
	example.Preparation = []plan.Command{{
		Run: &exampleInstall,
		Interpretations: []plan.Interpretation{{
			Capability: plan.CapabilityDependenciesInstall,
		}},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{root, example}), nil)
	if strings.Contains(got, "Projects: 2") {
		t.Fatalf("output %q, want examples excluded from the peer project count", got)
	}
	if strings.Contains(got, "Project: examples/friends") {
		t.Fatalf("output %q, want the example not listed as a peer project", got)
	}
	if strings.Count(got, "How to work with this project:") != 1 {
		t.Fatalf("output %q, want a single primary command section", got)
	}
	if !strings.Contains(got, "Example project:\n  examples/friends  (elixir, mix)") {
		t.Fatalf("output %q, want a compact example index", got)
	}
}

func TestWriteUsesRepositoryNameForTheRootHeading(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Languages = []plan.DetectedValue{{Name: "elixir"}}

	var buf bytes.Buffer
	Write(&buf, plan.NewDocument([]plan.ProjectPlan{root}), Options{RepositoryName: "ecto"})
	got := buf.String()
	if !strings.Contains(got, "ecto\n====") {
		t.Fatalf("output %q, want the repository name as the root heading", got)
	}
	if strings.Contains(got, "Repository root") || strings.Contains(got, "Project: .") {
		t.Fatalf("output %q, want the repository name instead of a generic root heading", got)
	}
}

func TestWriteKeepsNestedNonFixtureProjectsAsPeers(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Languages = []plan.DetectedValue{{Name: "go"}}
	frontend := plan.NewProjectPlan("frontend")
	frontend.Languages = []plan.DetectedValue{{Name: "javascript"}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{root, frontend}), nil)
	if !strings.Contains(got, "Projects: 2") {
		t.Fatalf("output %q, want a peer-project count", got)
	}
	if !strings.Contains(got, "Repository root\n===============") {
		t.Fatalf("output %q, want a human heading for the repository root", got)
	}
	if !strings.Contains(got, "Project: frontend\n=================") {
		t.Fatalf("output %q, want the nested project kept as a peer", got)
	}
}

func TestWriteExplainsWhenOnlyFixtureProjectsWereDetected(t *testing.T) {
	t.Parallel()

	fixture := plan.NewProjectPlan("fixtures/sample")
	fixture.Facts = []plan.ProjectFact{{
		Name:       "project.role",
		Value:      "fixture",
		Confidence: plan.ConfidenceHigh,
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{fixture}), []string{"node"})
	if !strings.Contains(got, "1 fixture project omitted; use --json to inspect") {
		t.Fatalf("output %q, want the omitted project notice", got)
	}
	if !strings.Contains(got, "No non-fixture project roots were detected.") {
		t.Fatalf("output %q, want an explanation for the empty human view", got)
	}
}

func TestWriteRendersACoveredNodeProject(t *testing.T) {
	t.Parallel()

	run := "npm test"
	install := "npm ci"
	id := plan.CommandID("cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	installID := plan.CommandID("cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	project := plan.NewProjectPlan(".")
	project.Languages = []plan.DetectedValue{{
		Name:       "javascript",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
	}}
	project.PackageManagers = []plan.DetectedTool{{
		Name:       "npm",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceFile, Source: "package-lock.json"}},
	}}
	project.Facts = []plan.ProjectFact{{
		Name:       "tool.configured",
		Value:      "eslint",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceConfiguration, Source: "eslint.config.js"}},
	}}
	project.Preparation = []plan.Command{{
		ID:         installID,
		Name:       "install dependencies",
		Run:        &install,
		Directory:  ".",
		Scope:      plan.ScopeProject,
		Origin:     plan.CommandInferred,
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceFile, Source: "package-lock.json"}},
		Interpretations: []plan.Interpretation{{
			Capability: plan.CapabilityDependenciesInstall,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceConvention, Source: "node-ecosystem", Pointer: "npm-ci"}},
		}},
		Variants: []plan.CommandVariant{},
	}}
	project.Commands = []plan.Command{{
		ID:         id,
		Name:       "test",
		Run:        &run,
		Directory:  ".",
		Scope:      plan.ScopeProject,
		Origin:     plan.CommandDeclared,
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json", Pointer: "/scripts/test"}},
		Interpretations: []plan.Interpretation{{
			Capability: plan.CapabilityTestRun,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json", Pointer: "/scripts/test"}},
		}},
		Variants: []plan.CommandVariant{{
			Context:    "ci",
			Run:        "npm test --coverage",
			Directory:  ".",
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml", Pointer: "/jobs/test/steps/2/run"}},
		}},
	}}

	required := true
	hasDefault := true
	project.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementEnvironment,
		Name:       "API_TOKEN",
		IsRequired: &required,
		HasDefault: &hasDefault,
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: ".env.example", Pointer: "/API_TOKEN"}},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"node"})
	for _, want := range []string{
		"Languages: javascript",
		"Package managers: npm",
		"npm ci",
		"npm test",
		"CI variant          npm test --coverage",
		"environment API_TOKEN (required, default present)",
		"eslint is configured. No command that invokes it was found.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "Providers:") {
		t.Fatalf("output %q, want no supported-provider catalog on a covered project", got)
	}
	if strings.Contains(got, "Project: .") {
		t.Fatalf("output %q, want no JSON-path project heading", got)
	}
	if strings.Contains(got, "Evidence:") {
		t.Fatalf("output %q, want evidence omitted by default", got)
	}
}

func TestWriteHidesEslintWhenDeclaredScriptInvokesIt(t *testing.T) {
	t.Parallel()

	run := "npm run lint"
	id := plan.CommandID("cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	project := plan.NewProjectPlan(".")
	project.Languages = []plan.DetectedValue{{Name: "javascript"}}
	project.Facts = []plan.ProjectFact{{
		Name:       "tool.configured",
		Value:      "eslint",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceConfiguration, Source: "eslint.config.js"}},
	}}
	project.Commands = []plan.Command{{
		ID:        id,
		Name:      "lint",
		Run:       &run,
		Directory: ".",
		Scope:     plan.ScopeProject,
		Origin:    plan.CommandDeclared,
		Interpretations: []plan.Interpretation{{
			Capability: plan.CapabilityCodeLint,
			Confidence: plan.ConfidenceHigh,
			Evidence: []plan.Evidence{{
				Kind:        plan.EvidenceDeclaration,
				Source:      "package.json",
				Pointer:     "/scripts/lint",
				Description: "The script invokes ESLint.",
			}},
		}},
		Variants: []plan.CommandVariant{},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"node"})
	if strings.Contains(got, "eslint is configured") {
		t.Fatalf("output %q, did not want an eslint gap when npm run lint invokes ESLint", got)
	}
}

func TestWriteHidesTscWhenDeclaredScriptInvokesTypeScript(t *testing.T) {
	t.Parallel()

	run := "npm run build"
	id := plan.CommandID("cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	project := plan.NewProjectPlan(".")
	project.Languages = []plan.DetectedValue{{Name: "javascript"}}
	project.Facts = []plan.ProjectFact{{
		Name:       "tool.configured",
		Value:      "tsc",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceConfiguration, Source: "tsconfig.json"}},
	}}
	project.Commands = []plan.Command{{
		ID:        id,
		Name:      "build",
		Run:       &run,
		Directory: ".",
		Scope:     plan.ScopeProject,
		Origin:    plan.CommandDeclared,
		Interpretations: []plan.Interpretation{{
			Capability: plan.CapabilityCodeTypecheck,
			Confidence: plan.ConfidenceHigh,
			Evidence: []plan.Evidence{{
				Kind:        plan.EvidenceDeclaration,
				Source:      "package.json",
				Pointer:     "/scripts/build",
				Description: "The script invokes the TypeScript compiler.",
			}},
		}},
		Variants: []plan.CommandVariant{},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"node"})
	if strings.Contains(got, "tsc is configured") {
		t.Fatalf("output %q, did not want a tsc gap when npm run build invokes the TypeScript compiler", got)
	}
}

func TestWriteReportsNextestWhenOnlyCargoTestExists(t *testing.T) {
	t.Parallel()

	run := "cargo test"
	id := plan.CommandID("cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	project := plan.NewProjectPlan(".")
	project.Languages = []plan.DetectedValue{{Name: "rust"}}
	project.Facts = []plan.ProjectFact{{
		Name:       "tool.configured",
		Value:      "nextest",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceConfiguration, Source: ".config/nextest.toml"}},
	}}
	project.Commands = []plan.Command{{
		ID:         id,
		Name:       "test",
		Run:        &run,
		Directory:  ".",
		Scope:      plan.ScopeProject,
		Origin:     plan.CommandInferred,
		Confidence: plan.ConfidenceHigh,
		Interpretations: []plan.Interpretation{{
			Capability: plan.CapabilityTestRun,
			Confidence: plan.ConfidenceHigh,
		}},
		Variants: []plan.CommandVariant{},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"rust"})
	if !strings.Contains(got, "nextest is configured. No command that invokes it was found.") {
		t.Fatalf("output %q, want nextest visible when only cargo test exists", got)
	}
}

func TestWriteHidesNextestWhenCargoNextestExists(t *testing.T) {
	t.Parallel()

	run := "cargo nextest run"
	id := plan.CommandID("cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	project := plan.NewProjectPlan(".")
	project.Languages = []plan.DetectedValue{{Name: "rust"}}
	project.Facts = []plan.ProjectFact{{
		Name:       "tool.configured",
		Value:      "nextest",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceConfiguration, Source: ".config/nextest.toml"}},
	}}
	project.Commands = []plan.Command{{
		ID:         id,
		Name:       "test",
		Run:        &run,
		Directory:  ".",
		Scope:      plan.ScopeProject,
		Origin:     plan.CommandObserved,
		Confidence: plan.ConfidenceHigh,
		Interpretations: []plan.Interpretation{{
			Capability: plan.CapabilityTestRun,
			Confidence: plan.ConfidenceHigh,
		}},
		Variants: []plan.CommandVariant{},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"rust"})
	if strings.Contains(got, "nextest is configured") {
		t.Fatalf("output %q, did not want a nextest gap when cargo nextest run exists", got)
	}
}

func TestWriteReportsConfiguredToolsWithoutCapabilityMapping(t *testing.T) {
	t.Parallel()

	project := plan.NewProjectPlan(".")
	project.Languages = []plan.DetectedValue{{Name: "rust"}}
	project.Facts = []plan.ProjectFact{{
		Name:       "tool.configured",
		Value:      "cargo-deny",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceConfiguration, Source: "deny.toml"}},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"rust"})
	if !strings.Contains(got, "cargo-deny is configured.") {
		t.Fatalf("output %q, want cargo-deny configured notice", got)
	}
}

func TestWriteHidesCargoDenyWhenCommandInvokesIt(t *testing.T) {
	t.Parallel()

	run := "cargo deny check"
	id := plan.CommandID("cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	project := plan.NewProjectPlan(".")
	project.Languages = []plan.DetectedValue{{Name: "rust"}}
	project.Facts = []plan.ProjectFact{{
		Name:       "tool.configured",
		Value:      "cargo-deny",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceConfiguration, Source: "deny.toml"}},
	}}
	project.Commands = []plan.Command{{
		ID:         id,
		Name:       "cargo deny",
		Run:        &run,
		Directory:  ".",
		Scope:      plan.ScopeProject,
		Origin:     plan.CommandObserved,
		Confidence: plan.ConfidenceHigh,
		Variants:   []plan.CommandVariant{},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"rust"})
	if strings.Contains(got, "cargo-deny is configured") {
		t.Fatalf("output %q, did not want a cargo-deny gap when cargo deny check exists", got)
	}
}

func TestWriteIncludesEvidenceWhenRequested(t *testing.T) {
	t.Parallel()

	project := plan.NewProjectPlan(".")
	project.Languages = []plan.DetectedValue{{
		Name:       "javascript",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
	}}

	got := renderDocumentWith(plan.NewDocument([]plan.ProjectPlan{project}), Options{ShowEvidence: true})
	if !strings.Contains(got, "Evidence:\n    package.json") {
		t.Fatalf("output %q, want evidence when requested", got)
	}
}

func TestWriteListsInterpretedCommandsFirst(t *testing.T) {
	t.Parallel()

	unknown := "pnpm run e2e:harness:coverage"
	testRun := "pnpm run test"
	lintRun := "pnpm run lint"
	project := plan.NewProjectPlan(".")
	project.Languages = []plan.DetectedValue{{
		Name:       "javascript",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
	}}
	project.Commands = []plan.Command{
		{
			ID:         plan.CommandID("cmd_11111111111111111111111111111111"),
			Name:       "e2e:harness:coverage",
			Run:        &unknown,
			Directory:  ".",
			Scope:      plan.ScopeProject,
			Origin:     plan.CommandDeclared,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
			Variants:   []plan.CommandVariant{},
		},
		{
			ID:         plan.CommandID("cmd_22222222222222222222222222222222"),
			Name:       "test",
			Run:        &testRun,
			Directory:  ".",
			Scope:      plan.ScopeProject,
			Origin:     plan.CommandDeclared,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
			Interpretations: []plan.Interpretation{{
				Capability: plan.CapabilityTestRun,
				Confidence: plan.ConfidenceHigh,
				Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
			}},
			Variants: []plan.CommandVariant{},
		},
		{
			ID:         plan.CommandID("cmd_33333333333333333333333333333333"),
			Name:       "lint",
			Run:        &lintRun,
			Directory:  ".",
			Scope:      plan.ScopeProject,
			Origin:     plan.CommandDeclared,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
			Interpretations: []plan.Interpretation{{
				Capability: plan.CapabilityCodeLint,
				Confidence: plan.ConfidenceHigh,
				Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
			}},
			Variants: []plan.CommandVariant{},
		},
	}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"node"})
	lintAt := strings.Index(got, "Lint")
	testAt := strings.Index(got, "Test")
	if lintAt < 0 || testAt < 0 {
		t.Fatalf("output %q, want lint and test", got)
	}
	if testAt >= lintAt {
		t.Fatalf("output %q, want lifecycle-ordered interpreted commands", got)
	}
	if strings.Contains(got, "e2e:harness:coverage") || strings.Contains(got, "Uninterpreted commands:") {
		t.Fatalf("output %q, want uninterpreted commands omitted by default", got)
	}

	got = renderDocumentWith(plan.NewDocument([]plan.ProjectPlan{project}), Options{Providers: []string{"node"}, ShowUninterpreted: true})
	unknownAt := strings.Index(got, "e2e:harness:coverage")
	lintAt = strings.Index(got, "Lint")
	testAt = strings.Index(got, "Test")
	if unknownAt < 0 {
		t.Fatalf("output %q, want uninterpreted commands when requested", got)
	}
	if testAt >= lintAt || lintAt >= unknownAt {
		t.Fatalf("output %q, want lifecycle-ordered interpreted commands before uninterpreted commands", got)
	}
}

func TestWriteKeepsMultilineCommandRunsOnOneLine(t *testing.T) {
	t.Parallel()

	run := "# into K buckets\n# suite leftover"
	project := plan.NewProjectPlan(".")
	project.Languages = []plan.DetectedValue{{
		Name:       "javascript",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
	}}
	project.Commands = []plan.Command{{
		ID:         plan.CommandID("cmd_cccccccccccccccccccccccccccccccc"),
		Name:       "# into",
		Run:        &run,
		Directory:  ".",
		Scope:      plan.ScopeProject,
		Origin:     plan.CommandObserved,
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml"}},
		Variants:   []plan.CommandVariant{},
	}}

	got := renderDocumentWith(plan.NewDocument([]plan.ProjectPlan{project}), Options{Providers: []string{"node"}, ShowUninterpreted: true})
	if strings.Contains(got, "\n# suite leftover") {
		t.Fatalf("output %q, want the run collapsed onto one line", got)
	}
	if !strings.Contains(got, "# into K buckets # suite leftover") {
		t.Fatalf("output %q, want collapsed run text", got)
	}
}

func TestWriteShowsDirectoryWhenItDiffersFromTheProject(t *testing.T) {
	t.Parallel()

	run := "docker compose up -d"
	project := plan.NewProjectPlan(".")
	project.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementService,
		Name:       "db",
		Version:    "13",
		Confidence: plan.ConfidenceHigh,
	}}
	project.Preparation = []plan.Command{{
		ID:         plan.CommandID("cmd_dddddddddddddddddddddddddddddddd"),
		Name:       "start services",
		Run:        &run,
		Directory:  "dev",
		Scope:      plan.ScopeProject,
		Origin:     plan.CommandInferred,
		Confidence: plan.ConfidenceHigh,
		Variants:   []plan.CommandVariant{},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"compose"})
	if !strings.Contains(got, "service db 13") {
		t.Fatalf("output %q, want requirement kind", got)
	}
	if !strings.Contains(got, "(in dev)") {
		t.Fatalf("output %q, want the compose directory when it differs from the project", got)
	}
}

func TestWriteOmitsVariantsThatRenderLikeThePrimaryCommand(t *testing.T) {
	t.Parallel()

	testRun := "npm test"
	project := plan.NewProjectPlan(".")
	project.Commands = []plan.Command{{
		ID:        plan.CommandID("cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
		Name:      "test",
		Run:       &testRun,
		Directory: ".",
		Interpretations: []plan.Interpretation{{
			Capability: plan.CapabilityTestRun,
		}},
		Variants: []plan.CommandVariant{
			{Context: "ci", Run: "npm test", Directory: "."},
			{Context: "ci", Run: "npm test --coverage", Directory: "."},
			{Context: "ci", Run: "npm test", Directory: "integration"},
		},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"node"})
	if strings.Contains(got, "CI variant  npm test\n") {
		t.Fatalf("output %q, want an identical CI invocation omitted", got)
	}
	if !strings.Contains(got, "CI variant  npm test --coverage\n") {
		t.Fatalf("output %q, want a CI invocation with different arguments", got)
	}
	if !strings.Contains(got, "CI variant  npm test  (in integration)\n") {
		t.Fatalf("output %q, want an invocation from a different directory", got)
	}
}

func TestWritePrioritizesInterpretedCommandsWithinDistinctProjectBlocks(t *testing.T) {
	t.Parallel()

	install := "pnpm install --frozen-lockfile"
	startServices := "docker compose up -d"
	testRun := "pnpm run test"
	unknownRun := "pnpm run generate:icons"
	frontend := plan.NewProjectPlan("frontend")
	frontend.Languages = []plan.DetectedValue{{
		Name:     "typescript",
		Evidence: []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "frontend/package.json"}},
	}}
	frontend.PackageManagers = []plan.DetectedTool{{Name: "pnpm", Version: "9"}}
	frontend.Requirements = []plan.Requirement{{Kind: plan.RequirementRuntime, Name: "node", Version: "22"}}
	frontend.Preparation = []plan.Command{
		{
			ID:   plan.CommandID("cmd_11111111111111111111111111111111"),
			Name: "install dependencies",
			Run:  &install,
			Interpretations: []plan.Interpretation{{
				Capability: plan.CapabilityDependenciesInstall,
			}},
		},
		{
			ID:   plan.CommandID("cmd_55555555555555555555555555555555"),
			Name: "start services",
			Run:  &startServices,
		},
	}
	frontend.Commands = []plan.Command{
		{
			ID:   plan.CommandID("cmd_22222222222222222222222222222222"),
			Name: "test",
			Run:  &testRun,
			Interpretations: []plan.Interpretation{{
				Capability: plan.CapabilityTestRun,
			}},
			Variants: []plan.CommandVariant{{Context: "ci", Run: "pnpm run test --run"}},
		},
		{
			ID:   plan.CommandID("cmd_33333333333333333333333333333333"),
			Name: "generate:icons",
			Run:  &unknownRun,
		},
	}
	testID := frontend.Commands[0].ID
	frontend.Ambiguities = []plan.Ambiguity{{
		Subject:   "command.test.run",
		CommandID: &testID,
		Message:   "More than one test invocation is plausible.",
		Candidates: []plan.Candidate{
			{Value: "pnpm run test"},
			{Value: "npm test"},
		},
	}}
	frontend.Conflicts = []plan.Conflict{{
		Subject:    "runtime.node.version",
		Message:    "Runtime declarations disagree.",
		Assertions: []plan.Candidate{{Value: "20"}, {Value: "22"}},
		Resolution: &plan.Resolution{
			SelectedValue: "22",
			Reason:        "The version file is more specific.",
		},
	}}

	backend := plan.NewProjectPlan("backend")
	backend.Languages = []plan.DetectedValue{{Name: "go"}}
	goTest := "go test ./..."
	backend.Commands = []plan.Command{{
		ID:   plan.CommandID("cmd_44444444444444444444444444444444"),
		Name: "test",
		Run:  &goTest,
		Interpretations: []plan.Interpretation{{
			Capability: plan.CapabilityTestRun,
		}},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{backend, frontend}), []string{"golang", "node"})
	for _, want := range []string{
		"Projects: 2",
		"Project: backend\n================",
		"Project: frontend\n=================",
		"  How to work with this project:\n    Purpose               Command\n    --------------------  -------",
		"    Install dependencies  pnpm install --frozen-lockfile",
		"    Prepare               docker compose up -d",
		"    Test                  pnpm run test",
		"      CI variant          pnpm run test --run",
		"  Needs attention:\n    Ambiguity: command.test.run",
		"      More than one test invocation is plausible.\n      - pnpm run test\n      - npm test",
		"    Conflict: runtime.node.version\n      Runtime declarations disagree.",
		"      Selected: 22 (The version file is more specific.)",
		"  Project details:\n    Languages: typescript\n    Package managers: pnpm 9",
		"    Requirements:\n      runtime node 22",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "Path:") {
		t.Fatalf("output %q, want the project path only in its heading", got)
	}
	if strings.Contains(got, "Uninterpreted commands:") || strings.Contains(got, "generate:icons") {
		t.Fatalf("output %q, want uninterpreted commands omitted by default", got)
	}
	if strings.Contains(got, "Evidence:") {
		t.Fatalf("output %q, want evidence omitted by default", got)
	}
	frontendOutput := got[strings.Index(got, "Project: frontend"):]
	interpretedAt := strings.Index(frontendOutput, "How to work with this project:")
	attentionAt := strings.Index(frontendOutput, "Needs attention:")
	detailsAt := strings.Index(frontendOutput, "Project details:")
	if interpretedAt < 0 || attentionAt < 0 || detailsAt < 0 || interpretedAt >= attentionAt || attentionAt >= detailsAt {
		t.Fatalf("output %q, want interpreted commands, attention items, then project details", got)
	}

	got = renderDocumentWith(plan.NewDocument([]plan.ProjectPlan{backend, frontend}), Options{
		Providers:         []string{"golang", "node"},
		ShowUninterpreted: true,
		ShowEvidence:      true,
	})
	if !strings.Contains(got, "  Uninterpreted commands:\n    Name            Command\n    --------------  -------\n    generate:icons  pnpm run generate:icons") {
		t.Fatalf("output %q, want uninterpreted commands when requested", got)
	}
	frontendOutput = got[strings.Index(got, "Project: frontend"):]
	interpretedAt = strings.Index(frontendOutput, "How to work with this project:")
	attentionAt = strings.Index(frontendOutput, "Needs attention:")
	uninterpretedAt := strings.Index(frontendOutput, "Uninterpreted commands:")
	detailsAt = strings.Index(frontendOutput, "Project details:")
	evidenceAt := strings.Index(frontendOutput, "Evidence:")
	if interpretedAt < 0 || attentionAt < 0 || uninterpretedAt < 0 || detailsAt < 0 || evidenceAt < 0 || interpretedAt >= attentionAt || attentionAt >= uninterpretedAt || uninterpretedAt >= detailsAt || detailsAt >= evidenceAt {
		t.Fatalf("output %q, want interpreted commands, attention items, uninterpreted commands, project details, then evidence", got)
	}
}

func renderDocument(document plan.Document, providers []string) string {
	return renderDocumentWith(document, Options{Providers: providers})
}

func renderDocumentWith(document plan.Document, opts Options) string {
	var buf bytes.Buffer
	Write(&buf, document, opts)
	return buf.String()
}
