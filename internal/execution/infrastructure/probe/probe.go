// Package probe executes inside the isolated runner container.
package probe

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"strconv"

	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
	"github.com/daidaidahaidai/local-mock/internal/mocking/infrastructure/grpcmock"
)

func Execute(ctx context.Context, call domain.ProbeCall) domain.ProbeResult {
	var result domain.ProbeResult
	switch call.Protocol {
	case "grpc":
		v, err := grpcmock.InvokeDescriptors(ctx, call.Address, call.Descriptors, call.Method, call.Request)
		result.Code, result.TraceID, result.Response = v.Code, v.TraceID, v.Response
		if err != nil {
			result.Error = err.Error()
		}
	case "http":
		result = HTTP(ctx, call.Address, call.Path)
	case "tcp":
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", call.Address)
		if err != nil {
			result.Error = err.Error()
		} else {
			conn.Close()
			result.Code = "OK"
		}
	default:
		result.Error = "unsupported probe protocol"
	}
	return result
}

func HTTP(ctx context.Context, address, path string) domain.ProbeResult {
	var result domain.ProbeResult
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+path, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	var trace [16]byte
	var span [8]byte
	if _, err = rand.Read(trace[:]); err != nil {
		result.Error = err.Error()
		return result
	}
	if _, err = rand.Read(span[:]); err != nil {
		result.Error = err.Error()
		return result
	}
	result.TraceID = hex.EncodeToString(trace[:])
	req.Header.Set("traceparent", "00-"+result.TraceID+"-"+hex.EncodeToString(span[:])+"-01")
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32769))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if len(b) > 32768 {
		result.Error = "response exceeds 32 KiB"
		return result
	}
	result.Code = strconv.Itoa(resp.StatusCode)
	result.Body = string(b)
	return result
}
