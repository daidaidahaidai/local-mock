package grpcmock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/daidaidahaidai/local-mock/internal/mocking/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func Serve(ctx context.Context, listener net.Listener, bundle domain.Bundle, events io.Writer) error {
	svc, err := Validate(bundle)
	if err != nil {
		return err
	}
	rules := map[string]domain.Rule{}
	for _, rule := range bundle.Rules {
		rules[rule.Method] = rule
	}
	server := grpc.NewServer()
	var mu sync.Mutex
	desc := grpc.ServiceDesc{ServiceName: bundle.Service, HandlerType: (*interface{})(nil)}
	for i := 0; i < svc.Methods().Len(); i++ {
		method := svc.Methods().Get(i)
		fullMethod := "/" + bundle.Service + "/" + string(method.Name())
		desc.Methods = append(desc.Methods, grpc.MethodDesc{MethodName: string(method.Name()), Handler: func(_ any, callCtx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			request := dynamicpb.NewMessage(method.Input())
			if err := decode(request); err != nil {
				return nil, err
			}
			handler := func(ctx context.Context, _ any) (response any, callErr error) {
				start := time.Now()
				rule, matched := rules[string(method.Name())]
				defer func() {
					if events != nil {
						traceID := ""
						if md, ok := metadata.FromIncomingContext(ctx); ok {
							values := md.Get("traceparent")
							if len(values) > 0 {
								parts := strings.Split(values[0], "-")
								if len(parts) == 4 {
									traceID = parts[1]
								}
							}
						}
						mu.Lock()
						_ = json.NewEncoder(events).Encode(map[string]any{"event": "mock_call", "mock": bundle.Name, "method": fullMethod, "matched": matched, "code": status.Code(callErr).String(), "duration_ms": time.Since(start).Milliseconds(), "trace_id": traceID})
						mu.Unlock()
					}
				}()
				if !matched {
					return nil, status.Error(codes.Unimplemented, "no mock rule for "+fullMethod)
				}
				if rule.DelayMS > 0 {
					timer := time.NewTimer(time.Duration(rule.DelayMS) * time.Millisecond)
					defer timer.Stop()
					select {
					case <-ctx.Done():
						return nil, status.FromContextError(ctx.Err()).Err()
					case <-timer.C:
					}
				}
				if rule.ErrorCode != "" {
					return nil, status.Error(errorCodes[rule.ErrorCode], rule.ErrorMessage)
				}
				message := dynamicpb.NewMessage(method.Output())
				if err := protojson.Unmarshal(rule.Response, message); err != nil {
					return nil, status.Error(codes.Internal, "invalid compiled response")
				}
				return message, nil
			}
			if interceptor != nil {
				return interceptor(callCtx, request, &grpc.UnaryServerInfo{FullMethod: fullMethod}, handler)
			}
			return handler(callCtx, request)
		}})
	}
	server.RegisterService(&desc, struct{}{})
	h := health.NewServer()
	healthpb.RegisterHealthServer(server, h)
	h.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	h.SetServingStatus(bundle.Service, healthpb.HealthCheckResponse_SERVING)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			server.Stop()
		case <-finished:
		}
	}()
	if err := server.Serve(listener); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

func CheckHealth(ctx context.Context, address string) error {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	r, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return err
	}
	if r.Status != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("service not serving: %s", r.Status)
	}
	return nil
}

type Invocation struct {
	TraceID  string          `json:"trace_id"`
	Response json.RawMessage `json:"response,omitempty"`
	Code     string          `json:"code"`
}

func Invoke(ctx context.Context, address, contract, fullMethod string, request []byte) (Invocation, error) {
	descriptors, _, err := Compile(ctx, contract)
	if err != nil {
		return Invocation{}, err
	}
	parts := strings.Split(strings.TrimPrefix(fullMethod, "/"), "/")
	if len(parts) != 2 {
		return Invocation{}, fmt.Errorf("method must be package.Service/Method")
	}
	svc, err := Service(descriptors, parts[0])
	if err != nil {
		return Invocation{}, err
	}
	method := svc.Methods().ByName(protoreflect.Name(parts[1]))
	if method == nil {
		return Invocation{}, fmt.Errorf("method not found")
	}
	if method.IsStreamingClient() || method.IsStreamingServer() {
		return Invocation{}, fmt.Errorf("streaming calls are not supported")
	}
	in := dynamicpb.NewMessage(method.Input())
	if err = protojson.Unmarshal(request, in); err != nil {
		return Invocation{}, err
	}
	var trace [16]byte
	var span [8]byte
	if _, err = rand.Read(trace[:]); err != nil {
		return Invocation{}, err
	}
	if _, err = rand.Read(span[:]); err != nil {
		return Invocation{}, err
	}
	result := Invocation{TraceID: hex.EncodeToString(trace[:])}
	ctx = metadata.AppendToOutgoingContext(ctx, "traceparent", "00-"+result.TraceID+"-"+hex.EncodeToString(span[:])+"-01")
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return result, err
	}
	defer conn.Close()
	out := dynamicpb.NewMessage(method.Output())
	err = conn.Invoke(ctx, "/"+parts[0]+"/"+parts[1], in, out)
	result.Code = status.Code(err).String()
	if err != nil {
		return result, err
	}
	result.Response, err = protojson.Marshal(out)
	return result, err
}
