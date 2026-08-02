package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// Exit codes. A help request is a success; a malformed invocation is
// distinguishable from work that failed, so a caller (a script, or an agent)
// can tell "you called it wrong" from "the operation failed" without parsing
// English.
const (
	OK      = 0
	Failure = 1
	Usage   = 2
)

// Wanted reports whether args request help.
//
// Only `-h` and `--help` count, and the scan stops at the first `--`. Both
// halves matter. Stopping at `--` is what lets `git work git demo -- log -h`
// forward -h to git verbatim. Excluding the bare word "help" is what lets
// `git work rm help` address a repo named "help" and `git work add --repos
// help` name it as a flag value - no positional and no flag value can begin
// with a dash, so -h and --help are unambiguous everywhere.
//
// Groups whose first argument is a subcommand slot accept "help" as a verb too;
// they check for it with IsHelpVerb, because only there is it unambiguous.
func Wanted(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

// IsHelpVerb reports whether a group's first argument asks for help. Groups
// hold a verb in that position, so the bare word is safe there.
func IsHelpVerb(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

// Help prints a command's usage to stdout and returns OK. Help that was asked
// for goes to stdout; usage printed because the caller erred goes to stderr,
// via Fail.
func Help(c *Command) int { return HelpText(c.Render()) }

// HelpText prints an already-rendered block to stdout and returns OK.
func HelpText(s string) int { return helpTo(os.Stdout, s) }

func helpTo(w io.Writer, s string) int {
	fmt.Fprintln(w, s)
	return OK
}

// usageError marks an error as "the invocation was malformed" rather than "the
// work failed". Fail maps the two to different exit codes.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

// Usagef builds a usage error: the caller got the invocation wrong, so Fail
// will print the command's help alongside it and exit 2.
func Usagef(format string, a ...any) error {
	return usageError{fmt.Errorf(format, a...)}
}

// IsUsage reports whether err came from Usagef.
func IsUsage(err error) bool {
	var u usageError
	return errors.As(err, &u)
}

// Fail reports an error and returns the process exit code: Usage (2) plus the
// command's help for a malformed invocation, Failure (1) alone for work that
// failed. c may be nil when no command context is available.
func Fail(err error, c *Command) int {
	var usageText string
	if c != nil {
		usageText = c.Render()
	}
	return FailText(err, usageText)
}

// FailText is Fail for a dispatcher that holds an index rather than a Command.
func FailText(err error, usageText string) int {
	return failTo(os.Stderr, err, usageText)
}

func failTo(w io.Writer, err error, usageText string) int {
	fmt.Fprintf(w, "git-work: %v\n", err)
	if !IsUsage(err) {
		return Failure
	}
	if usageText != "" {
		fmt.Fprintf(w, "\n%s\n", usageText)
	}
	return Usage
}
