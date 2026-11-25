package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/swiftly-headquarters/takumi/generators/golang"
	"github.com/swiftly-headquarters/takumi/generators/typescript"
	"github.com/swiftly-headquarters/takumi/protospec"
	"github.com/swiftly-headquarters/takumi/spec"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var generators = map[string]func(*spec.Document) (map[string][]byte, error){
	"go":         golang.Generate,
	"typescript": typescript.Generate,
}

var grpcGenerators = map[string]func(protoreflect.FileDescriptor) (map[string][]byte, error){
	"go":         golang.GenerateGRPC,
	"typescript": typescript.GenerateGRPC,
}

func main() {
	input := flag.String("input", "", "OpenAPI 3.x or Swagger 2.0 spec (.json, .yaml, .yml file or http(s) URL), or a .proto file for a gRPC SDK")
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
	var files map[string][]byte
	var err error
	if strings.HasSuffix(input, ".proto") {
		files, err = runGRPC(input, lang, plan)
	} else {
		files, err = runOpenAPI(input, lang, plan)
	}
	if err != nil || files == nil {
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

func runOpenAPI(input, lang string, plan bool) (map[string][]byte, error) {
	generate, ok := generators[lang]
	if !ok {
		return nil, fmt.Errorf("unknown -lang %q (have: %s)", lang, strings.Join(spec.SortedKeys(generators), ", "))
	}
	doc, err := spec.Load(input)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", input, err)
	}
	if plan {
		for _, r := range doc.Plan() {
			name := "api." + strings.Join(append(slices.Clone(r.Resource), r.Method), ".")
			fmt.Printf("%-52s %-7s %s\n", name, strings.ToUpper(r.Op.Method), r.Op.Path)
		}
		return nil, nil
	}
	return generate(doc)
}

func runGRPC(input, lang string, plan bool) (map[string][]byte, error) {
	generate, ok := grpcGenerators[lang]
	if !ok {
		return nil, fmt.Errorf("unknown -lang %q for .proto input (have: %s)", lang, strings.Join(spec.SortedKeys(grpcGenerators), ", "))
	}
	fd, err := protospec.Load(input)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", input, err)
	}
	if plan {
		for i := 0; i < fd.Services().Len(); i++ {
			s := fd.Services().Get(i)
			for j := 0; j < s.Methods().Len(); j++ {
				fmt.Printf("%s.%s\n", s.Name(), s.Methods().Get(j).Name())
			}
		}
		return nil, nil
	}
	return generate(fd)
}
