// Package cli is the command line of kartograph: the commands, the answer document on stdout and the error document
// on stderr with the exit code (ADR-0003).
package cli

import (
	"errors"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hakastein/kartograph/internal/ladybug"
	"github.com/hakastein/kartograph/internal/store"
	"github.com/hakastein/kartograph/internal/workspace"
)

// The closed vocabulary of error codes (ADR-0003).
const (
	codeBadUsage        = "bad_usage"
	codeNotAWorkspace   = "not_a_workspace"
	codeCaptureUnstable = "capture_unstable"
	codeQueryRejected   = "query_rejected"
	codeNotFound        = "not_found"
	codeInternal        = "internal"
)

// fault is an error of a command: its code, a one-line message and the keys that follow them.
type fault struct {
	code    string
	message string
	details mapping
}

func (f *fault) Error() string {
	return f.code + ": " + f.message
}

func (f *fault) document() mapping {
	return append(mapping{{key: "code", value: f.code}, {key: "message", value: f.message}}, f.details...)
}

func (f *fault) exitCode() int {
	if f.code == codeBadUsage {
		return 2
	}
	return 1
}

// Run runs kartograph with argv in the working directory dir and returns the exit code. env is the environment of
// the process: the store root and git's configuration come from it.
func Run(argv, env []string, dir string, stdout, stderr io.Writer) int {
	c := &call{env: env, dir: dir}
	root := c.command()
	root.SetArgs(argv)
	root.SetOut(stdout)
	root.SetErr(io.Discard)
	err := root.Execute()
	if err == nil {
		if c.answer != nil {
			io.WriteString(stdout, render(c.answer))
		}
		return 0
	}
	var f *fault
	if !errors.As(err, &f) {
		f = &fault{code: codeBadUsage, message: err.Error()}
	}
	io.WriteString(stderr, render(f.document()))
	return f.exitCode()
}

// call is one run of kartograph: where it runs and the answer of its command.
type call struct {
	env    []string
	dir    string
	answer mapping
}

func (c *call) command() *cobra.Command {
	root := &cobra.Command{
		Use:           "kartograph",
		Short:         "Build a graph of a worktree's code and answer questions about it",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return &fault{code: codeBadUsage, message: "a command is required"}
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(
		&cobra.Command{
			Use:   "index",
			Short: "Capture the workspace and publish its snapshot",
			Args:  cobra.NoArgs,
			RunE:  func(*cobra.Command, []string) error { return c.run(c.index) },
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show the published snapshot of the workspace",
			Args:  cobra.NoArgs,
			RunE:  func(*cobra.Command, []string) error { return c.run(c.status) },
		},
		&cobra.Command{
			Use:   "query <cypher>",
			Short: "Run a read-only Cypher query on the snapshot",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				return c.run(func() (mapping, error) { return c.query(args[0]) })
			},
		},
	)
	return root
}

func (c *call) run(command func() (mapping, error)) error {
	answer, err := command()
	if err != nil {
		return c.fault(err)
	}
	c.answer = answer
	return nil
}

func (c *call) fault(err error) *fault {
	var f *fault
	var unstable *workspace.Unstable
	switch {
	case errors.As(err, &f):
		return f
	case errors.Is(err, workspace.ErrNotWorkspace):
		return &fault{code: codeNotAWorkspace, message: err.Error(), details: mapping{{key: "path", value: c.dir}}}
	case errors.As(err, &unstable):
		return &fault{code: codeCaptureUnstable, message: err.Error(),
			details: mapping{{key: "attempts", value: int64(unstable.Attempts)}}}
	case errors.Is(err, store.ErrNoSnapshot):
		return &fault{code: codeNotFound, message: err.Error()}
	}
	return &fault{code: codeInternal, message: oneLine(err.Error())}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (c *call) getenv(name string) string {
	for _, kv := range c.env {
		if value, ok := strings.CutPrefix(kv, name+"="); ok {
			return value
		}
	}
	return ""
}

// open finds the workspace of the call and opens the store.
func (c *call) open() (*workspace.Workspace, *store.Store, error) {
	ws, err := workspace.Find(c.dir, c.env)
	if err != nil {
		return nil, nil, err
	}
	root, err := store.Root(c.getenv)
	if err != nil {
		return nil, nil, err
	}
	st, err := store.Open(root)
	if err != nil {
		return nil, nil, err
	}
	return ws, st, nil
}

func snapshotBlock(s store.Snapshot) mapping {
	var head any
	if s.Head != "" {
		head = s.Head
	}
	return mapping{
		{key: "workspace", value: s.Workspace},
		{key: "generation", value: s.Generation},
		{key: "head", value: head},
		{key: "dirty", value: s.Dirty},
	}
}

// rejected is a query the engine or the read-only rule refused.
func rejected(err error) error {
	var r *ladybug.Rejected
	if errors.As(err, &r) {
		return &fault{code: codeQueryRejected, message: oneLine(r.Reason)}
	}
	return err
}
