package protospec

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func Load(path string) (protoreflect.FileDescriptor, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	c := protocompile.Compiler{
		Resolver:       protocompile.WithStandardImports(&protocompile.SourceResolver{ImportPaths: []string{filepath.Dir(abs)}}),
		SourceInfoMode: protocompile.SourceInfoStandard,
	}
	files, err := c.Compile(context.Background(), filepath.Base(abs))
	if err != nil {
		return nil, err
	}
	return files[0], nil
}

type Types struct {
	Messages []protoreflect.MessageDescriptor
	Enums    []protoreflect.EnumDescriptor
}

func Reachable(fd protoreflect.FileDescriptor) Types {
	var t Types
	seen := map[protoreflect.FullName]bool{}
	var msg func(protoreflect.MessageDescriptor)
	enum := func(e protoreflect.EnumDescriptor) {
		if !seen[e.FullName()] {
			seen[e.FullName()] = true
			t.Enums = append(t.Enums, e)
		}
	}
	msg = func(m protoreflect.MessageDescriptor) {
		if seen[m.FullName()] {
			return
		}
		seen[m.FullName()] = true
		t.Messages = append(t.Messages, m)
		for i := 0; i < m.Fields().Len(); i++ {
			f := m.Fields().Get(i)
			if f.IsMap() {
				f = f.MapValue()
			}
			if f.Message() != nil {
				msg(f.Message())
			}
			if f.Enum() != nil {
				enum(f.Enum())
			}
		}
	}
	for i := 0; i < fd.Services().Len(); i++ {
		ms := fd.Services().Get(i).Methods()
		for j := 0; j < ms.Len(); j++ {
			msg(ms.Get(j).Input())
			msg(ms.Get(j).Output())
		}
	}
	byName := func(a, b protoreflect.FullName) int { return strings.Compare(string(a), string(b)) }
	slices.SortFunc(t.Messages, func(a, b protoreflect.MessageDescriptor) int { return byName(a.FullName(), b.FullName()) })
	slices.SortFunc(t.Enums, func(a, b protoreflect.EnumDescriptor) int { return byName(a.FullName(), b.FullName()) })
	return t
}

func LocalName(d protoreflect.Descriptor) string {
	return strings.ReplaceAll(strings.TrimPrefix(string(d.FullName()), string(d.ParentFile().Package())+"."), ".", "_")
}

func Comment(d protoreflect.Descriptor) string {
	lines := strings.Split(strings.TrimSpace(d.ParentFile().SourceLocations().ByDescriptor(d).LeadingComments), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, " ")
	}
	return strings.Join(lines, "\n")
}
