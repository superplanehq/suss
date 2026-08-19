package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	suss "github.com/superplanehq/suss"
	"github.com/superplanehq/suss/render"
)

var errHelp = errors.New("help")

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args)
	if errors.Is(err, errHelp) {
		fmt.Fprint(stdout, usage())
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "suss:", err)
		fmt.Fprint(stderr, usage())
		return 2
	}

	document, err := suss.Detect(opts.path)
	if err != nil {
		fmt.Fprintln(stderr, "suss:", err)
		return 1
	}

	if opts.json {
		encoded, err := document.MarshalCanonical()
		if err != nil {
			fmt.Fprintln(stderr, "suss:", err)
			return 1
		}
		_, _ = stdout.Write(encoded)
		return 0
	}

	render.Write(stdout, document, render.Options{
		Providers:           suss.Providers(),
		RepositoryName:      repositoryName(opts.path),
		ShowAllCommands:     opts.allCommands,
		ShowAllProjects:     opts.allProjects,
		ShowAllEnvironments: opts.allEnvironments,
		ShowUninterpreted:   opts.uninterpreted,
		ShowEvidence:        opts.evidence,
	})
	return 0
}

type options struct {
	path            string
	json            bool
	allCommands     bool
	allProjects     bool
	allEnvironments bool
	uninterpreted   bool
	evidence        bool
}

func parseArgs(args []string) (options, error) {
	opts := options{path: "."}
	pathSet := false

	for _, arg := range args {
		switch {
		case arg == "--json":
			opts.json = true
		case arg == "--all-commands":
			opts.allCommands = true
		case arg == "--all-projects":
			opts.allProjects = true
		case arg == "--all-environments":
			opts.allEnvironments = true
		case arg == "--uninterpreted":
			opts.uninterpreted = true
		case arg == "--evidence":
			opts.evidence = true
		case arg == "-h", arg == "--help":
			return options{}, errHelp
		case strings.HasPrefix(arg, "-"):
			return options{}, fmt.Errorf("unknown flag %s", arg)
		default:
			if pathSet {
				return options{}, fmt.Errorf("unexpected extra argument %s", arg)
			}
			opts.path = arg
			pathSet = true
		}
	}

	return opts, nil
}

func repositoryName(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Base(path)
	}
	return filepath.Base(absolute)
}

func usage() string {
	return `Usage: suss [path] [--json] [--all-commands] [--all-projects] [--all-environments] [--uninterpreted] [--evidence]

Inspect a repository and emit how to set it up, build it, test it, and run it.

  path                 repository to inspect (default: .)
  --json               emit the versioned plan document
  --all-commands       include every interpreted command
  --all-projects       include every detected project
  --all-environments   include every detected Compose environment
  --uninterpreted      include commands without a known purpose
  --evidence           include source files that support the plan

Without --json, a human-readable rendering of the plan is printed.
`
}
