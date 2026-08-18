// Package java detects Maven and Gradle JVM projects from pom.xml and Gradle
// build files. Detection is static and never invokes Maven or Gradle.
package java

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

const providerName = "java"

// Provider detects one Maven and/or Gradle project.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects one project root. It returns no findings without a Maven or
// Gradle manifest.
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	project, err := inspectProject(ctx)
	if err != nil || !project.present() {
		return provider.Result{}, err
	}

	result := provider.Result{Findings: projectFindings(ctx, project)}
	runtimes, conflicts, err := runtimeFindings(ctx, project)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, runtimes...)
	result.Conflicts = append(result.Conflicts, conflicts...)
	result.Findings = append(result.Findings, configuredToolFindings(ctx, project)...)

	commands, ambiguities, err := commandFindings(ctx, project)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, commands...)
	result.Ambiguities = append(result.Ambiguities, ambiguities...)
	return result, nil
}

type javaProject struct {
	Maven  *mavenProject
	Gradle *gradleProject
}

func (p javaProject) present() bool {
	return p.Maven != nil || p.Gradle != nil
}

func (p javaProject) competingManagers() bool {
	return p.Maven != nil && p.Gradle != nil
}

func inspectProject(ctx provider.Context) (javaProject, error) {
	maven, err := readMaven(ctx)
	if err != nil {
		return javaProject{}, err
	}
	gradle, err := readGradle(ctx)
	if err != nil {
		return javaProject{}, err
	}
	return javaProject{Maven: maven, Gradle: gradle}, nil
}

func projectFindings(ctx provider.Context, project javaProject) []plan.Finding {
	findings := []plan.Finding{
		propertyFinding(ctx, plan.PropertyLanguage, "java", "", "", languageEvidence(ctx, project)),
	}
	if project.Maven != nil {
		findings = append(findings, packageManagerFinding(ctx, "maven", project.Maven.WrapperVersion, mavenManagerEvidence(ctx, project.Maven)))
		if project.Maven.Aggregator {
			findings = append(findings, factFinding(ctx, "workspace.orchestrator", "maven", []plan.Evidence{{
				Kind:    plan.EvidenceDeclaration,
				Source:  ctx.SourcePath(project.Maven.Source),
				Pointer: "/modules",
			}}))
		}
	}
	if project.Gradle != nil {
		findings = append(findings, packageManagerFinding(ctx, "gradle", project.Gradle.WrapperVersion, gradleManagerEvidence(ctx, project.Gradle)))
		if project.Gradle.MultiProject {
			findings = append(findings, factFinding(ctx, "workspace.orchestrator", "gradle", []plan.Evidence{{
				Kind:   plan.EvidenceConfiguration,
				Source: ctx.SourcePath(project.Gradle.SettingsFile),
			}}))
		}
	}
	if evidence := springBootEvidence(ctx, project); len(evidence) > 0 {
		findings = append(findings, propertyFinding(ctx, plan.PropertyFramework, "spring-boot", "", "", evidence))
	}
	return findings
}

func languageEvidence(ctx provider.Context, project javaProject) []plan.Evidence {
	var evidence []plan.Evidence
	if project.Maven != nil {
		evidence = append(evidence, plan.Evidence{Kind: plan.EvidenceDeclaration, Source: ctx.SourcePath(project.Maven.Source)})
	}
	if project.Gradle != nil {
		evidence = append(evidence, plan.Evidence{Kind: plan.EvidenceDeclaration, Source: ctx.SourcePath(project.Gradle.Source)})
	}
	return evidence
}

func mavenManagerEvidence(ctx provider.Context, maven *mavenProject) []plan.Evidence {
	evidence := []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: ctx.SourcePath(maven.Source)}}
	if maven.WrapperProperties != "" {
		evidence = append(evidence, plan.Evidence{
			Kind:    plan.EvidenceDeclaration,
			Source:  maven.WrapperProperties,
			Pointer: "/distributionUrl",
		})
	}
	return evidence
}

func gradleManagerEvidence(ctx provider.Context, gradle *gradleProject) []plan.Evidence {
	evidence := []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: ctx.SourcePath(gradle.Source)}}
	if gradle.WrapperProperties != "" {
		evidence = append(evidence, plan.Evidence{
			Kind:    plan.EvidenceDeclaration,
			Source:  ctx.SourcePath(gradle.WrapperProperties),
			Pointer: "/distributionUrl",
		})
	}
	return evidence
}

func springBootEvidence(ctx provider.Context, project javaProject) []plan.Evidence {
	var evidence []plan.Evidence
	if project.Maven != nil {
		evidence = append(evidence, project.Maven.springBootEvidence(ctx)...)
	}
	if project.Gradle != nil {
		evidence = append(evidence, project.Gradle.springBootEvidence(ctx)...)
	}
	return evidence
}

func propertyFinding(ctx provider.Context, kind plan.PropertyKind, name, value, version string, evidence []plan.Evidence) plan.Finding {
	return plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       kind,
			Name:       name,
			Value:      value,
			Version:    version,
			Confidence: plan.ConfidenceHigh,
			Evidence:   evidence,
		},
	}
}

func packageManagerFinding(ctx provider.Context, name, version string, evidence []plan.Evidence) plan.Finding {
	return propertyFinding(ctx, plan.PropertyPackageManager, name, "", version, evidence)
}

func factFinding(ctx provider.Context, name, value string, evidence []plan.Evidence) plan.Finding {
	return propertyFinding(ctx, plan.PropertyFact, name, value, "", evidence)
}

func fileExists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
	return err == nil && !info.IsDir()
}

func dirExists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
	return err == nil && info.IsDir()
}

func stringPtr(value string) *string {
	return &value
}

func pointerToken(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}
