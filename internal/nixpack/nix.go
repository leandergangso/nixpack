package nixpack

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const defaultStoreDir = "/nix/store"

type stringSet map[string]struct{}

func newStringSet(paths ...string) stringSet {
	s := make(stringSet, len(paths))
	s.addAll(paths)
	return s
}

func (s stringSet) add(path string) {
	s[path] = struct{}{}
}

func (s stringSet) addAll(paths []string) {
	for _, p := range paths {
		s.add(p)
	}
}

func (s stringSet) sorted() []string {
	out := make([]string, 0, len(s))
	for p := range s {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

func storeDir() string {
	if dir := os.Getenv("NIX_STORE_DIR"); dir != "" {
		return strings.TrimRight(dir, "/")
	}
	return defaultStoreDir
}

// storePath expands basenames returned by newer versions of `nix derivation show`.
func storePath(name string) string {
	if strings.HasPrefix(name, "/") {
		return name
	}
	return filepath.Join(storeDir(), name)
}

func requireBin(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is required but was not found in PATH: %w", name, err)
	}
	return nil
}

// run executes a command and captures both stdout and stderr.
func run(name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.Command(name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return nil, fmt.Errorf("%s %s failed: %w\n%s", name, strings.Join(args, " "), err, msg)
	}
	return stdout.Bytes(), nil
}

// runStream executes a command while forwarding its stderr to the user. Used
// for long-running commands (nix build, nix copy) that print useful progress.
func runStream(name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.Command(name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

func logln(a ...any) {
	fmt.Fprintln(os.Stderr, a...)
}

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format, a...)
}
