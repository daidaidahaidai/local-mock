package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/daidaidahaidai/local-mock/internal/mocking/domain"
	"github.com/daidaidahaidai/local-mock/internal/mocking/infrastructure/grpcmock"
)

func (c CLI) mock(ctx context.Context, args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("usage: localmock mock serve|health")
	}
	if args[0] == "health" {
		f := flag.NewFlagSet("mock health", flag.ContinueOnError)
		f.SetOutput(c.Err)
		address := f.String("address", "127.0.0.1:50051", "gRPC address")
		if err := f.Parse(args[1:]); err != nil {
			return 2, err
		}
		if f.NArg() > 0 {
			return 2, fmt.Errorf("unexpected positional arguments")
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		err := grpcmock.CheckHealth(ctx, *address)
		return exit(err), err
	}
	if args[0] != "serve" {
		return 2, fmt.Errorf("unknown mock command")
	}
	f := flag.NewFlagSet("mock serve", flag.ContinueOnError)
	f.SetOutput(c.Err)
	file := f.String("f", "", "mock YAML")
	bundlePath := f.String("bundle", "", "compiled mock bundle")
	listen := f.String("listen", "127.0.0.1:50051", "listen address")
	if err := f.Parse(args[1:]); err != nil {
		return 2, err
	}
	if f.NArg() > 0 || (*file == "") == (*bundlePath == "") {
		return 2, fmt.Errorf("specify exactly one of -f or --bundle")
	}
	var bundle domain.Bundle
	if *file != "" {
		loaded, _, _, err := grpcmock.Load(ctx, *file)
		if err != nil {
			return 2, err
		}
		bundle = loaded
	} else {
		b, err := os.ReadFile(*bundlePath)
		if err != nil {
			return 2, err
		}
		if len(b) > 32<<20 {
			return 2, fmt.Errorf("bundle exceeds 32 MiB")
		}
		if err = json.Unmarshal(b, &bundle); err != nil {
			return 2, err
		}
	}
	if _, err := grpcmock.Validate(bundle); err != nil {
		return 2, err
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return 1, err
	}
	defer listener.Close()
	fmt.Fprintln(c.Err, "mock listening:", listener.Addr())
	err = grpcmock.Serve(ctx, listener, bundle, c.Out)
	return exit(err), err
}
func (c CLI) rpc(ctx context.Context, args []string) (int, error) {
	f := flag.NewFlagSet("rpc", flag.ContinueOnError)
	f.SetOutput(c.Err)
	address := f.String("address", "", "gRPC server address")
	contract := f.String("proto", "", ".proto contract file")
	method := f.String("method", "", "package.Service/Method")
	data := f.String("data", "{}", "protobuf JSON request")
	dataFile := f.String("data-file", "", "JSON request file")
	timeout := f.Duration("timeout", 10*time.Second, "RPC timeout")
	if err := f.Parse(args); err != nil {
		return 2, err
	}
	if f.NArg() > 0 || *address == "" || *contract == "" || *method == "" || *timeout <= 0 {
		return 2, fmt.Errorf("rpc requires --address, --proto, --method and positive timeout")
	}
	request := []byte(*data)
	if *dataFile != "" {
		if *data != "{}" {
			return 2, fmt.Errorf("use --data or --data-file")
		}
		var err error
		request, err = os.ReadFile(*dataFile)
		if err != nil {
			return 2, err
		}
	}
	if len(request) > 4<<20 {
		return 2, fmt.Errorf("request exceeds 4 MiB")
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	result, err := grpcmock.Invoke(ctx, *address, *contract, *method, request)
	if result.TraceID != "" {
		if outputErr := c.json(result); outputErr != nil {
			return 1, outputErr
		}
	}
	return exit(err), err
}
