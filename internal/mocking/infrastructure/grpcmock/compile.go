package grpcmock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/bufbuild/protocompile"
	"github.com/daidaidahaidai/local-mock/internal/mocking/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"gopkg.in/yaml.v3"
)

type config struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Protocol string `yaml:"protocol"`
	Contract string `yaml:"contract"`
	Service  string `yaml:"service"`
	Rules    []struct {
		Method   string         `yaml:"method"`
		Response map[string]any `yaml:"response"`
		Error    *struct {
			Code    string `yaml:"code"`
			Message string `yaml:"message"`
		} `yaml:"error"`
		DelayMS int `yaml:"delayMs"`
	} `yaml:"rules"`
}

func Load(ctx context.Context, path string) (domain.Bundle, map[string]string, string, error) {
	var c config
	data, err := read(path)
	if err != nil {
		return domain.Bundle{}, nil, "", err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err = decoder.Decode(&c); err != nil {
		return domain.Bundle{}, nil, "", err
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return domain.Bundle{}, nil, "", fmt.Errorf("mock definition must contain exactly one YAML document")
	}
	if c.APIVersion != "localmock/v1alpha1" || c.Kind != "Mock" || c.Protocol != "grpc" || c.Metadata.Name == "" || c.Contract == "" || c.Service == "" {
		return domain.Bundle{}, nil, "", fmt.Errorf("expected named v1alpha1 gRPC Mock with contract and service")
	}
	contract := c.Contract
	if !filepath.IsAbs(contract) {
		contract = filepath.Join(filepath.Dir(path), contract)
	}
	set, inputs, err := Compile(ctx, contract)
	if err != nil {
		return domain.Bundle{}, nil, "", err
	}
	inputs[path] = fmt.Sprintf("%x", sha256.Sum256(data))
	bundle := domain.Bundle{Name: c.Metadata.Name, Service: c.Service, Descriptors: set}
	for _, r := range c.Rules {
		rule := domain.Rule{Method: r.Method, DelayMS: r.DelayMS}
		if (r.Response == nil) == (r.Error == nil) {
			return bundle, nil, "", fmt.Errorf("method %s requires exactly one of response or error", r.Method)
		}
		if r.Response != nil {
			rule.Response, err = json.Marshal(r.Response)
			if err != nil {
				return bundle, nil, "", err
			}
		}
		if r.Error != nil {
			rule.ErrorCode = r.Error.Code
			rule.ErrorMessage = r.Error.Message
		}
		bundle.Rules = append(bundle.Rules, rule)
	}
	if _, err = Validate(bundle); err != nil {
		return bundle, nil, "", err
	}
	return bundle, inputs, contract, nil
}

func Compile(ctx context.Context, path string) ([]byte, map[string]string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	root := filepath.Dir(absolute)
	inputs := map[string]string{}
	var mu sync.Mutex
	resolver := &protocompile.SourceResolver{Accessor: func(name string) (io.ReadCloser, error) {
		if !fs.ValidPath(name) {
			return nil, fmt.Errorf("invalid proto import %q", name)
		}
		full := filepath.Join(root, filepath.FromSlash(name))
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || !fs.ValidPath(filepath.ToSlash(rel)) {
			return nil, fmt.Errorf("proto import escapes contract directory")
		}
		b, err := read(resolved)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		inputs[resolved] = fmt.Sprintf("%x", sha256.Sum256(b))
		mu.Unlock()
		return io.NopCloser(bytes.NewReader(b)), nil
	}}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver)}
	files, err := compiler.Compile(ctx, filepath.Base(absolute))
	if err != nil {
		return nil, nil, err
	}
	set := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	var visit func(protoreflect.FileDescriptor)
	visit = func(f protoreflect.FileDescriptor) {
		if seen[f.Path()] {
			return
		}
		seen[f.Path()] = true
		for i := 0; i < f.Imports().Len(); i++ {
			visit(f.Imports().Get(i).FileDescriptor)
		}
		set.File = append(set.File, protodesc.ToFileDescriptorProto(f))
	}
	for _, f := range files {
		visit(f)
	}
	sort.Slice(set.File, func(i, j int) bool { return set.File[i].GetName() < set.File[j].GetName() })
	b, err := (proto.MarshalOptions{Deterministic: true}).Marshal(set)
	return b, inputs, err
}

func Service(descriptors []byte, name string) (protoreflect.ServiceDescriptor, error) {
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(descriptors, &set); err != nil {
		return nil, err
	}
	files, err := protodesc.NewFiles(&set)
	if err != nil {
		return nil, err
	}
	d, err := files.FindDescriptorByName(protoreflect.FullName(name))
	if err != nil {
		return nil, err
	}
	svc, ok := d.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%s is not a service", name)
	}
	return svc, nil
}
func Validate(bundle domain.Bundle) (protoreflect.ServiceDescriptor, error) {
	if bundle.Service == "grpc.health.v1.Health" {
		return nil, fmt.Errorf("health service is reserved by the mock runtime")
	}
	svc, err := Service(bundle.Descriptors, bundle.Service)
	if err != nil {
		return nil, err
	}
	for i := 0; i < svc.Methods().Len(); i++ {
		m := svc.Methods().Get(i)
		if m.IsStreamingClient() || m.IsStreamingServer() {
			return nil, fmt.Errorf("streaming method %s is not supported", m.FullName())
		}
	}
	seen := map[string]bool{}
	for _, rule := range bundle.Rules {
		method := svc.Methods().ByName(protoreflect.Name(rule.Method))
		if method == nil {
			return nil, fmt.Errorf("unknown method %s.%s", bundle.Service, rule.Method)
		}
		if seen[rule.Method] {
			return nil, fmt.Errorf("duplicate rule for method %s", rule.Method)
		}
		seen[rule.Method] = true
		if rule.DelayMS < 0 || rule.DelayMS > 300000 {
			return nil, fmt.Errorf("delayMs must be 0..300000")
		}
		if len(rule.Response) > 0 {
			if rule.ErrorCode != "" {
				return nil, fmt.Errorf("response and error cannot be combined")
			}
			if err := protojson.Unmarshal(rule.Response, dynamicpb.NewMessage(method.Output())); err != nil {
				return nil, fmt.Errorf("response for %s: %w", rule.Method, err)
			}
		} else if _, ok := errorCodes[rule.ErrorCode]; !ok {
			return nil, fmt.Errorf("invalid gRPC error code %s", rule.ErrorCode)
		}
	}
	return svc, nil
}

var errorCodes = map[string]codes.Code{"Cancelled": codes.Canceled, "Canceled": codes.Canceled, "Unknown": codes.Unknown, "InvalidArgument": codes.InvalidArgument, "DeadlineExceeded": codes.DeadlineExceeded, "NotFound": codes.NotFound, "AlreadyExists": codes.AlreadyExists, "PermissionDenied": codes.PermissionDenied, "ResourceExhausted": codes.ResourceExhausted, "FailedPrecondition": codes.FailedPrecondition, "Aborted": codes.Aborted, "OutOfRange": codes.OutOfRange, "Unimplemented": codes.Unimplemented, "Internal": codes.Internal, "Unavailable": codes.Unavailable, "DataLoss": codes.DataLoss, "Unauthenticated": codes.Unauthenticated}

func read(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 4<<20 {
		return nil, fmt.Errorf("mock/proto file exceeds 4 MiB")
	}
	return b, nil
}
