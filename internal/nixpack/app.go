package nixpack

import (
	"bufio"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	program       = "nixpack"
	defaultOutput = program + ".tar"
)

type options struct {
	Out       string
	File      string
	BuildDeps bool
	NoBuild   bool
}

// Run is the CLI entry point.
func Run(args []string, version string) error {
	opts, args, fs := parseFlags(args, version)

	specs, err := collectSpecs(args, opts.File)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		fs.Usage()
		return nil
	}
	if opts.Out == "" {
		return fmt.Errorf("output path must not be empty")
	}
	if err := requireBin("nix"); err != nil {
		return err
	}
	if err := requireBin("nix-store"); err != nil {
		return err
	}

	started := time.Now()

	plain, nixFiles := splitInstallables(specs)

	logln("==> Resolving derivations")
	top, err := resolveAll(plain, nixFiles, false)
	if err != nil {
		return err
	}
	for _, drvPath := range top.sorted() {
		logf("  %s\n", drvPath)
	}

	graph := top
	if opts.BuildDeps {
		logln("==> Resolving build dependency graph")
		graph, err = resolveAll(plain, nixFiles, true)
		if err != nil {
			return err
		}
		logf("  %d derivations in closure\n", len(graph))
	}

	paths := newStringSet()
	outputs := newStringSet()
	for drvPath := range top {
		paths.add(drvPath)
	}

	if opts.NoBuild {
		logln("==> Skipping build (-no-build)")
	} else {
		logf("==> Building %d installable(s)\n", len(specs))
		results, err := buildAll(plain, nixFiles)
		if err != nil {
			return err
		}
		for _, r := range results {
			if r.DrvPath != "" {
				paths.add(r.DrvPath)
			}
			for _, p := range r.Outputs {
				if p != "" {
					outputs.add(p)
				}
			}
		}
	}

	// Derivation outputs cover store-path installables and the -no-build case.
	for _, d := range top {
		outputs.addAll(d.outputs)
	}

	if opts.BuildDeps {
		for _, d := range graph {
			paths.add(d.path)
			paths.addAll(d.srcs)
			outputs.addAll(d.outputs)
		}
	}

	// Use nix path-info so unregistered leftovers from failed builds are not
	// included in the cache.
	registered, err := registeredPaths(outputs.sorted())
	if err != nil {
		return err
	}
	outputs = newStringSet(registered...)
	paths.addAll(outputs.sorted())

	logf("==> Computing store closure (%d seed paths)\n", len(paths))
	closure, err := storeClosure(paths.sorted())
	if err != nil {
		return err
	}
	paths.addAll(closure)

	logf("==> Writing binary cache to %s (%d store paths)\n", opts.Out, len(paths))
	if err := writeCache(paths.sorted(), opts.Out); err != nil {
		return err
	}

	logf("\nSuccess: Packed %d store paths into %s in %.2fs\n",
		len(paths), opts.Out, time.Since(started).Seconds())
	if isTar(opts.Out) {
		logf("On the target: tar -xf %s -C <directory>\n", opts.Out)
		logf("Then: nix copy --all --no-check-sigs --from file://<directory>\n")
	} else {
		logf("On the target: nix copy --all --no-check-sigs --from file://<path-to-%s>\n", filepath.Base(opts.Out))
	}
	return nil
}

func splitInstallables(specs []string) (plain, nixFiles []string) {
	for _, spec := range specs {
		if filepath.Ext(spec) == ".nix" {
			nixFiles = append(nixFiles, spec)
		} else {
			plain = append(plain, spec)
		}
	}
	return plain, nixFiles
}

func resolveAll(plain, nixFiles []string, recursive bool) (drvGraph, error) {
	graph := drvGraph{}
	if len(plain) > 0 {
		resolved, err := resolveDerivations(plain, recursive)
		if err != nil {
			return nil, err
		}
		maps.Copy(graph, resolved)
	}
	for _, file := range nixFiles {
		resolved, err := resolveDerivationFile(file, recursive)
		if err != nil {
			return nil, err
		}
		maps.Copy(graph, resolved)
	}
	return graph, nil
}

func buildAll(plain, nixFiles []string) ([]buildResult, error) {
	var results []buildResult
	if len(plain) > 0 {
		built, err := buildInstallables(plain)
		if err != nil {
			return nil, err
		}
		results = append(results, built...)
	}
	for _, file := range nixFiles {
		built, err := buildFile(file)
		if err != nil {
			return nil, err
		}
		results = append(results, built...)
	}
	return results, nil
}

func parseFlags(args []string, version string) (options, []string, *flag.FlagSet) {
	fs := flag.NewFlagSet(program, flag.ExitOnError)
	fs.SetOutput(os.Stderr)

	showVersion := fs.Bool("version", false, "print version and exit")

	var opts options
	fs.StringVar(&opts.Out, "out", defaultOutput, "output .tar archive or directory")
	fs.StringVar(&opts.File, "file", "", "file with installables (one per line)")
	fs.BoolVar(&opts.BuildDeps, "build-deps", false, "include outputs of all build-time dependencies")
	fs.BoolVar(&opts.NoBuild, "no-build", false, "do not build installables locally (pack derivations only)")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "%s %s — Bundle Nix derivations into a portable binary cache.\n\n", program, version)
		fmt.Fprintf(fs.Output(), "Usage:\n")
		fmt.Fprintf(fs.Output(), "  %s [flags] <installable...>\n\n", program)
		fmt.Fprintf(fs.Output(), "Examples:\n")
		fmt.Fprintf(fs.Output(), "  %s nixpkgs#hello\n", program)
		fmt.Fprintf(fs.Output(), "  %s .#app -build-deps -out ./cache\n", program)
		fmt.Fprintf(fs.Output(), "  %s -file packages.txt -out nixpack.tar\n", program)
		fmt.Fprintf(fs.Output(), "  %s -no-build /nix/store/...-hello.drv\n\n", program)
		fmt.Fprintf(fs.Output(), "Flags:\n")
		fs.PrintDefaults()
	}

	_ = fs.Parse(args)
	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}
	return opts, fs.Args(), fs
}

// collectSpecs turns command line arguments into installables. Existing
// regular files are read as lists of installables, except for .nix files,
// which are installables themselves.
func collectSpecs(args []string, file string) ([]string, error) {
	var specs []string

	add := func(spec string) {
		spec = strings.TrimSpace(spec)
		if spec == "" || strings.HasPrefix(spec, "#") || slices.Contains(specs, spec) {
			return
		}
		specs = append(specs, spec)
	}

	for _, arg := range args {
		if isSpecFile(arg) {
			lines, err := readSpecsFile(arg)
			if err != nil {
				return nil, err
			}
			for _, line := range lines {
				add(line)
			}
			continue
		}
		add(arg)
	}

	if file != "" {
		lines, err := readSpecsFile(file)
		if err != nil {
			return nil, err
		}
		for _, line := range lines {
			add(line)
		}
	}
	return specs, nil
}

// isSpecFile reports whether an argument should be read as a list of
// installables. Derivations and store paths are installables, not lists.
func isSpecFile(arg string) bool {
	info, err := os.Stat(arg)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	switch filepath.Ext(arg) {
	case ".nix", ".drv":
		return false
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return true
	}
	return !strings.HasPrefix(abs, storeDir()+string(os.PathSeparator))
}

func readSpecsFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var specs []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		specs = append(specs, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return specs, nil
}
