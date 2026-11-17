package nixpack

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// derivation is the small subset of a Nix derivation we care about.
type derivation struct {
	path    string
	outputs []string
	srcs    []string
}

type drvGraph map[string]*derivation

func (g drvGraph) sorted() []string {
	out := make([]string, 0, len(g))
	for path := range g {
		out = append(out, path)
	}
	slices.Sort(out)
	return out
}

// rawDerivation supports both the classic and the v4 derivation JSON formats
// emitted by `nix derivation show`.
type rawDerivation struct {
	Outputs map[string]struct {
		Path string `json:"path"`
	} `json:"outputs"`
	InputSrcs []string `json:"inputSrcs"`
	Inputs    struct {
		Srcs []string `json:"srcs"`
	} `json:"inputs"`
}

type buildResult struct {
	DrvPath string            `json:"drvPath"`
	Outputs map[string]string `json:"outputs"`
}

// resolveDerivations evaluates installables into derivations. With recursive
// set, the full input graph (build-time dependencies) is returned.
func resolveDerivations(specs []string, recursive bool) (drvGraph, error) {
	args := []string{"derivation", "show", "--no-pretty"}
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, specs...)

	out, err := run("nix", args, nil)
	if err != nil {
		return nil, fmt.Errorf("resolve derivations: %w", err)
	}
	return parseDerivationGraph(out)
}

// resolveDerivationFile handles legacy .nix files, which need --file.
func resolveDerivationFile(path string, recursive bool) (drvGraph, error) {
	args := []string{"derivation", "show", "--no-pretty"}
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, "--file", path)
	out, err := run("nix", args, nil)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", path, err)
	}
	return parseDerivationGraph(out)
}

func parseDerivationGraph(data []byte) (drvGraph, error) {
	var wrapped struct {
		Derivations map[string]json.RawMessage `json:"derivations"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return nil, fmt.Errorf("parse derivation output: %w", err)
	}

	raw := wrapped.Derivations
	if raw == nil {
		// Classic format: { "/nix/store/...drv": { ... } }
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse derivation output: %w", err)
		}
	}

	graph := make(drvGraph, len(raw))
	for key, value := range raw {
		var d rawDerivation
		if err := json.Unmarshal(value, &d); err != nil {
			return nil, fmt.Errorf("parse derivation %s: %w", key, err)
		}

		item := &derivation{path: storePath(key)}
		for _, o := range d.Outputs {
			if o.Path != "" {
				item.outputs = append(item.outputs, storePath(o.Path))
			}
		}
		for _, src := range append(d.InputSrcs, d.Inputs.Srcs...) {
			if src != "" {
				item.srcs = append(item.srcs, storePath(src))
			}
		}
		slices.Sort(item.outputs)
		slices.Sort(item.srcs)
		graph[item.path] = item
	}
	return graph, nil
}

// buildInstallables builds the installables locally so their outputs (and the
// outputs of their dependencies) exist in the store before packing.
func buildInstallables(specs []string) ([]buildResult, error) {
	args := append([]string{"build", "--no-link", "--json"}, specs...)
	out, err := runStream("nix", args, nil)
	if err != nil {
		return nil, fmt.Errorf("build installables: %w", err)
	}
	return parseBuildOutput(out)
}

// buildFile builds a legacy .nix file.
func buildFile(path string) ([]buildResult, error) {
	out, err := runStream("nix", []string{"build", "--no-link", "--json", "--file", path}, nil)
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", path, err)
	}
	return parseBuildOutput(out)
}

func parseBuildOutput(data []byte) ([]buildResult, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("parse nix build output: %w", err)
	}

	results := make([]buildResult, 0, len(items))
	for _, item := range items {
		var r buildResult
		if err := json.Unmarshal(item, &r); err == nil && r.DrvPath != "" {
			results = append(results, r)
			continue
		}
		// A .drv installable is reported as a plain string.
		var drvPath string
		if err := json.Unmarshal(item, &drvPath); err != nil {
			return nil, fmt.Errorf("parse nix build output: %w", err)
		}
		results = append(results, buildResult{DrvPath: drvPath})
	}
	return results, nil
}

// registeredPaths filters out store paths that are not registered in the
// local store (e.g. leftovers from a failed build).
func registeredPaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	input := []byte(strings.Join(paths, "\n") + "\n")
	out, err := run("nix", []string{"path-info", "--json", "--json-format", "2", "--stdin"}, input)
	if err != nil {
		// Older Nix versions do not have --json-format.
		out, err = run("nix", []string{"path-info", "--json", "--stdin"}, input)
		if err != nil {
			return nil, fmt.Errorf("query path info: %w", err)
		}
	}

	var response struct {
		Info     map[string]json.RawMessage `json:"info"`
		StoreDir string                     `json:"storeDir"`
	}
	if err := json.Unmarshal(out, &response); err != nil {
		return nil, fmt.Errorf("parse path info: %w", err)
	}

	set := newStringSet()
	if response.Info == nil {
		// Classic format: { "/nix/store/path": { ... } }
		var classic map[string]json.RawMessage
		if err := json.Unmarshal(out, &classic); err != nil {
			return nil, fmt.Errorf("parse path info: %w", err)
		}
		for path, info := range classic {
			if string(info) != "null" {
				set.add(path)
			}
		}
		return set.sorted(), nil
	}

	dir := response.StoreDir
	if dir == "" {
		dir = storeDir()
	}
	for name, info := range response.Info {
		if string(info) != "null" {
			set.add(filepath.Join(dir, name))
		}
	}
	return set.sorted(), nil
}

// storeClosure returns the transitive closure of the given store paths.
func storeClosure(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	set := newStringSet()
	const chunkSize = 512
	for start := 0; start < len(paths); start += chunkSize {
		end := min(start+chunkSize, len(paths))
		args := append([]string{"-qR"}, paths[start:end]...)
		out, err := run("nix-store", args, nil)
		if err != nil {
			return nil, fmt.Errorf("query store closure: %w", err)
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				set.add(line)
			}
		}
	}
	return set.sorted(), nil
}
