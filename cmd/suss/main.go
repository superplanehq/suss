package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	suss "github.com/superplanehq/suss"
	"github.com/superplanehq/suss/plan"
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

	writeProjectList(stdout, document.Projects)
	return 0
}

type options struct {
	path string
	json bool
}

func parseArgs(args []string) (options, error) {
	opts := options{path: "."}
	pathSet := false

	for _, arg := range args {
		switch {
		case arg == "--json":
			opts.json = true
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

func writeProjectList(w io.Writer, projects []plan.ProjectPlan) {
	if len(projects) == 0 {
		fmt.Fprintln(w, "(no projects detected)")
		return
	}
	for _, project := range projects {
		fmt.Fprintln(w, project.Path)
	}
}

func usage() string {
	return `Usage: suss [path] [--json]

Inspect a repository and emit how to set it up, build it, test it, and run it.

  path     repository to inspect (default: .)
  --json   emit the versioned plan document

Without --json, project paths discovered in the document are listed.
Human-readable rendering of the full plan lands in a later milestone.
`
}
