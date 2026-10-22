package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const maxProtoBytes = 32 << 20

// protoSchema is an immutable, local descriptor registry. It never guesses a
// message type from its wire fields: gRPC uses service/method descriptors and
// raw protobuf uses the type the person chooses.
type protoSchema struct {
	files *protoregistry.Files
	types *dynamicpb.Types
	names []string
}

func readProtoFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxProtoBytes+1))
	if err == nil && len(data) > maxProtoBytes {
		err = fmt.Errorf("protobuf inputs are limited to %s", humanBytes(maxProtoBytes))
	}
	return data, err
}

func parseProtoSchema(data []byte) (*protoSchema, error) {
	if len(data) > maxProtoBytes {
		return nil, fmt.Errorf("descriptor set is larger than %s", humanBytes(maxProtoBytes))
	}
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("read FileDescriptorSet: %w", err)
	}
	if len(set.File) == 0 {
		return nil, fmt.Errorf("descriptor set contains no files (use protoc --include_imports --descriptor_set_out)")
	}
	files, err := protodesc.NewFiles(&set)
	if err != nil {
		return nil, fmt.Errorf("link descriptors; include all imports: %w", err)
	}
	s := &protoSchema{files: files, types: dynamicpb.NewTypes(files)}
	var messages func(protoreflect.MessageDescriptors)
	messages = func(list protoreflect.MessageDescriptors) {
		for i := range list.Len() {
			m := list.Get(i)
			if !m.IsMapEntry() {
				s.names = append(s.names, string(m.FullName()))
			}
			messages(m.Messages())
		}
	}
	files.RangeFiles(func(f protoreflect.FileDescriptor) bool {
		messages(f.Messages())
		return true
	})
	slices.Sort(s.names)
	return s, nil
}

// importProtoSchema compiles .proto in-process (no protoc executable or shell).
// Standard imports are bundled; other imports use the source directory and
// the explicit include roots, just like protoc -I.
func importProtoSchema(ctx context.Context, path string, roots []string) (*protoSchema, []byte, error) {
	if !strings.EqualFold(filepath.Ext(path), ".proto") {
		data, err := readProtoFile(path)
		if err != nil {
			return nil, nil, err
		}
		s, err := parseProtoSchema(data)
		return s, data, err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	resolver := &protocompile.SourceResolver{
		ImportPaths: append([]string{filepath.Dir(path)}, roots...),
		Accessor: func(path string) (io.ReadCloser, error) {
			data, err := readProtoFile(path)
			if err != nil {
				return nil, err
			}
			return io.NopCloser(strings.NewReader(string(data))), nil
		},
	}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver), MaxParallelism: 2}
	compiled, err := compiler.Compile(ctx, filepath.Base(path))
	if err != nil {
		return nil, nil, err
	}
	var set descriptorpb.FileDescriptorSet
	seen := make(map[string]bool)
	var add func(protoreflect.FileDescriptor)
	add = func(f protoreflect.FileDescriptor) {
		if seen[f.Path()] {
			return
		}
		seen[f.Path()] = true
		for i := range f.Imports().Len() {
			add(f.Imports().Get(i).FileDescriptor)
		}
		set.File = append(set.File, protodesc.ToFileDescriptorProto(f))
	}
	for _, f := range compiled {
		add(f)
	}
	data, err := proto.Marshal(&set)
	if err != nil {
		return nil, nil, err
	}
	s, err := parseProtoSchema(data)
	return s, data, err
}

func (s *protoSchema) message(name, path string, response bool) (protoreflect.MessageDescriptor, error) {
	if s == nil {
		return nil, nil
	}
	if name != "" && name != "Auto" {
		d, err := s.files.FindDescriptorByName(protoreflect.FullName(strings.TrimPrefix(name, ".")))
		if err != nil {
			return nil, err
		}
		m, ok := d.(protoreflect.MessageDescriptor)
		if !ok {
			return nil, fmt.Errorf("%s is not a message type", name)
		}
		return m, nil
	}
	service, method, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !ok {
		return nil, nil
	}
	d, err := s.files.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return nil, nil // A schema for another service must not guess a type.
	}
	svc, ok := d.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, nil
	}
	m := svc.Methods().ByName(protoreflect.Name(method))
	if m == nil {
		return nil, nil
	}
	if response {
		return m.Output(), nil
	}
	return m.Input(), nil
}
