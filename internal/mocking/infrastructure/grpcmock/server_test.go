package grpcmock

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/daidaidahaidai/local-mock/internal/mocking/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDynamicMockResponseHealthMissingRuleAndTrace(t *testing.T) {
	bundle, inputs, _, err := Load(context.Background(), "testdata/payment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 {
		t.Fatalf("inputs: %v", inputs)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, listener, bundle, &events) }()
	callCtx, callCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer callCancel()
	if err := CheckHealth(callCtx, listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	result, err := Invoke(callCtx, listener.Addr().String(), "testdata/demo.proto", "fixture.Payment/Charge", []byte(`{"id":"example"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != "OK" || !bytes.Contains(result.Response, []byte("mock-transaction")) || len(result.TraceID) != 32 {
		t.Fatalf("%+v", result)
	}
	_, err = Invoke(callCtx, listener.Addr().String(), "testdata/demo.proto", "fixture.Payment/Refund", []byte(`{}`))
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("missing rule: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(events.String(), result.TraceID) || !strings.Contains(events.String(), `"matched":false`) {
		t.Fatalf("missing call evidence: %s", events.String())
	}
}
func TestMockRulesValidatedAgainstProto(t *testing.T) {
	bundle, _, _, err := Load(context.Background(), "testdata/payment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for name, rule := range map[string]domain.Rule{
		"unknown-method": {Method: "NoSuchMethod", Response: []byte(`{}`)},
		"unknown-field":  {Method: "Charge", Response: []byte(`{"typo":true}`)},
		"invalid-status": {Method: "Charge", ErrorCode: "SUCCESS"},
		"both":           {Method: "Charge", Response: []byte(`{}`), ErrorCode: "Unavailable"},
	} {
		t.Run(name, func(t *testing.T) {
			copy := bundle
			copy.Rules = []domain.Rule{rule}
			if _, err := Validate(copy); err == nil {
				t.Fatal("accepted invalid rule")
			}
		})
	}
}
func TestDelayHonorsCallerDeadline(t *testing.T) {
	bundle, _, _, err := Load(context.Background(), "testdata/payment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	bundle.Rules[0].DelayMS = 500
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, l, bundle, nil) }()
	callCtx, callCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer callCancel()
	_, err = Invoke(callCtx, l.Addr().String(), "testdata/demo.proto", "fixture.Payment/Charge", json.RawMessage(`{}`))
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("%v", err)
	}
	cancel()
	<-done
}
