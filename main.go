package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/swiftly-headquarters/takumi/generators/typescript"
	"github.com/swiftly-headquarters/takumi/spec"
)

var generators = map[string]func(*spec.Document) (map[string][]byte, error){
	"typescript": typescript.Generate,
}

func main() {
	input := flag.String("input", "", "OpenAPI 3.x or Swagger 2.0 spec: .json, .yaml or .yml file, or http(s) URL")
	output := flag.String("output", "sdk", "directory the SDK is written to")
	lang := flag.String("lang", "typescript", "target language: "+strings.Join(spec.SortedKeys(generators), ", "))
	plan := flag.Bool("plan", false, "print the resource.method name of every operation and exit")
	flag.Parse()

	if *input == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*input, *output, *lang, *plan); err != nil {
		fmt.Fprintln(os.Stderr, "takumi:", err)
		os.Exit(1)
	}
}

func run(input, output, lang string, plan bool) error {
	generate, ok := generators[lang]
	if !ok {
		return fmt.Errorf("unknown -lang %q (have: %s)", lang, strings.Join(spec.SortedKeys(generators), ", "))
	}
	doc, err := spec.Load(input)
	if err != nil {
		return fmt.Errorf("%s: %w", input, err)
	}
	if plan {
		for _, r := range doc.Plan() {
			name := "api." + strings.Join(append(slices.Clone(r.Resource), r.Method), ".")
			fmt.Printf("%-52s %-7s %s\n", name, strings.ToUpper(r.Op.Method), r.Op.Path)
		}
		return nil
	}
	files, err := generate(doc)
	if err != nil {
		return err
	}
	for _, name := range spec.SortedKeys(files) {
		path := filepath.Join(output, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", path)
	}
	return nil
}
