package application

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
)

type Prober interface {
	Probe(context.Context, domain.Run, domain.Probe) (domain.ProbeResult, error)
}

func ResolveProbe(spec domain.Spec, p domain.Probe) (domain.ProbeCall, error) {
	w, ok := spec.Workloads[p.Deployment]
	if !ok {
		return domain.ProbeCall{}, fmt.Errorf("unknown deployment or component %s", p.Deployment)
	}
	e, ok := w.Endpoints[p.Interface]
	if !ok {
		return domain.ProbeCall{}, fmt.Errorf("unknown endpoint %s", p.Interface)
	}
	call := domain.ProbeCall{Protocol: e.Protocol, Address: net.JoinHostPort(p.Deployment, strconv.Itoa(e.Port)), Descriptors: e.Descriptors, Method: p.Method, Request: p.Request, Path: p.Path}
	if len(p.Request) > 32768 {
		return call, fmt.Errorf("probe request exceeds 32 KiB")
	}
	switch e.Protocol {
	case "http":
		if call.Path == "" {
			call.Path = "/"
		}
		u, err := url.ParseRequestURI(call.Path)
		if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(call.Path, "/") || strings.HasPrefix(call.Path, "//") || p.Method != "" || len(p.Request) > 0 {
			return call, fmt.Errorf("HTTP probe requires a local path and no RPC method/body")
		}
	case "grpc":
		if p.Path != "" || !strings.HasPrefix(p.Method, e.Service+"/") || len(e.Descriptors) == 0 {
			return call, fmt.Errorf("gRPC method must belong to the declared contract")
		}
		if len(call.Request) == 0 {
			call.Request = jsonObject
		}
	case "tcp":
		if p.Path != "" || p.Method != "" || len(p.Request) > 0 {
			return call, fmt.Errorf("TCP probe accepts no path, method or body")
		}
	default:
		return call, fmt.Errorf("probe protocol %s unsupported", e.Protocol)
	}
	return call, nil
}

var jsonObject = []byte("{}")
