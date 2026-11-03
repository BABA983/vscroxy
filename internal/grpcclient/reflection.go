package grpcclient

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	reflection "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Reflect returns a self-contained descriptor set for all advertised services.
// It prefers v1, falling back to wire-compatible v1alpha only for Unimplemented.
// Metadata is sent to reflection too; discovery never invokes service methods.
func Reflect(ctx context.Context, conn grpc.ClientConnInterface, md metadata.MD) ([]byte, error) {
	if err := validateMetadata(md); err != nil {
		return nil, err
	}
	data, err := reflectVersion(ctx, conn, md, "v1")
	if status.Code(err) == codes.Unimplemented {
		return reflectVersion(ctx, conn, md, "v1alpha")
	}
	return data, err
}

func reflectVersion(ctx context.Context, conn grpc.ClientConnInterface, md metadata.MD, version string) ([]byte, error) {
	ctx, cancel := context.WithCancel(metadata.NewOutgoingContext(ctx, md.Copy()))
	defer cancel()
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true, ServerStreams: true},
		"/grpc.reflection."+version+".ServerReflection/ServerReflectionInfo", grpc.MaxCallRecvMsgSize(32<<20))
	if err != nil {
		return nil, err
	}
	defer stream.CloseSend()
	query := func(req *reflection.ServerReflectionRequest) (*reflection.ServerReflectionResponse, error) {
		// SendMsg can report EOF before the status is available to RecvMsg.
		if err := stream.SendMsg(req); err != nil {
			var response reflection.ServerReflectionResponse
			if recvErr := stream.RecvMsg(&response); recvErr != nil {
				return nil, recvErr
			}
			return nil, err
		}
		var response reflection.ServerReflectionResponse
		if err := stream.RecvMsg(&response); err != nil {
			return nil, err
		}
		if problem := response.GetErrorResponse(); problem != nil {
			return nil, status.Error(codes.Code(problem.ErrorCode), problem.ErrorMessage)
		}
		return &response, nil
	}
	response, err := query(&reflection.ServerReflectionRequest{MessageRequest: &reflection.ServerReflectionRequest_ListServices{ListServices: ""}})
	if err != nil {
		return nil, err
	}
	if response.GetListServicesResponse() == nil {
		return nil, fmt.Errorf("reflection returned no service list")
	}
	services := response.GetListServicesResponse().Service
	if len(services) > 1024 {
		return nil, fmt.Errorf("reflection exceeds 1024 services")
	}
	files := map[string]*descriptorpb.FileDescriptorProto{}
	size := 0
	add := func(response *reflection.ServerReflectionResponse) error {
		if response.GetFileDescriptorResponse() == nil {
			return fmt.Errorf("reflection returned no file descriptors")
		}
		for _, data := range response.GetFileDescriptorResponse().FileDescriptorProto {
			size += len(data)
			if size > 32<<20 || len(files) >= 1024 {
				return fmt.Errorf("reflection exceeds 32 MiB or 1024 files")
			}
			var f descriptorpb.FileDescriptorProto
			if err := proto.Unmarshal(data, &f); err != nil {
				return err
			}
			if f.GetName() == "" {
				return fmt.Errorf("reflection returned an unnamed file")
			}
			if old, ok := files[f.GetName()]; ok && !proto.Equal(old, &f) {
				return fmt.Errorf("reflection returned conflicting descriptors for %s", f.GetName())
			}
			files[f.GetName()] = &f
		}
		return nil
	}
	for _, service := range services {
		response, err := query(&reflection.ServerReflectionRequest{MessageRequest: &reflection.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: service.GetName()}})
		if err != nil {
			return nil, err
		}
		if err := add(response); err != nil {
			return nil, err
		}
	}
	// Some servers return transitive imports with each file; others only
	// return the requested file. Resolve missing dependencies explicitly.
	for {
		missing := ""
		for _, f := range files {
			for _, dependency := range f.Dependency {
				if files[dependency] == nil {
					missing = dependency
					break
				}
			}
			if missing != "" {
				break
			}
		}
		if missing == "" {
			break
		}
		response, err := query(&reflection.ServerReflectionRequest{MessageRequest: &reflection.ServerReflectionRequest_FileByFilename{FileByFilename: missing}})
		if err != nil {
			return nil, err
		}
		if err := add(response); err != nil {
			return nil, err
		}
		if files[missing] == nil {
			return nil, fmt.Errorf("reflection omitted dependency %s", missing)
		}
	}
	var set descriptorpb.FileDescriptorSet
	for _, f := range files {
		set.File = append(set.File, f)
	}
	slices.SortFunc(set.File, func(a, b *descriptorpb.FileDescriptorProto) int {
		return cmp.Compare(a.GetName(), b.GetName())
	})
	if len(set.File) == 0 {
		return nil, fmt.Errorf("reflection returned no descriptors")
	}
	if _, err := protodesc.NewFiles(&set); err != nil {
		return nil, fmt.Errorf("link reflected descriptors: %w", err)
	}
	return proto.Marshal(&set)
}
