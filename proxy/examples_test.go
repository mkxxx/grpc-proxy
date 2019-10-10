// Copyright 2017-2018 Valient Gough
// Copyright 2017 Michal Witkowski
// All Rights Reserved.
// See LICENSE for licensing terms.

package proxy_test

import (
	"context"
	"strings"

	"github.com/mkxxx/grpc-proxy/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

var (
	director proxy.StreamDirector
)

func ExampleRegisterService() {
	// A gRPC server with the proxying codec enabled.
	server := grpc.NewServer(grpc.CustomCodec(proxy.Codec()))
	// Register a TestService with 4 of its methods explicitly.
	proxy.RegisterService(server, director,
		"vgough.testproto.TestService",
		"PingEmpty", "Ping", "PingError", "PingList")
}

func ExampleTransparentHandler() {
	grpc.NewServer(
		grpc.CustomCodec(proxy.Codec()),
		grpc.UnknownServiceHandler(proxy.TransparentHandler(director)))
}

// Provide sa simple example of a director that shields internal services and dials a staging or production backend.
// This is a *very naive* implementation that creates a new connection on every request. Consider using pooling.
type ExampleDirector struct {
}

func ClientConn(ctx context.Context, method string) (context.Context, context.CancelFunc, *grpc.ClientConn, error) {
	// Make sure we never forward internal services.
	if strings.HasPrefix(method, "/com.example.internal.") {
		return nil, nil, nil, grpc.Errorf(codes.Unimplemented, "Unknown method")
	}
	var addr string
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		// Decide on which backend to dial
		if val, exists := md[":authority"]; exists && val[0] == "staging.api.example.com" {
			addr = "api-service.staging.svc.local"
		} else if val, exists := md[":authority"]; exists && val[0] == "api.example.com" {
			addr = "api-service.prod.svc.local"
		}
	}
	if len(addr) == 0 {
		return nil, nil, nil, grpc.Errorf(codes.Unimplemented, "Unknown method")
	}
	conn, err := grpc.DialContext(ctx, addr, grpc.WithCodec(proxy.Codec()))
	return context.Background(), nil, conn, err
}
