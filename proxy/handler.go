// Copyright 2017-2018 Valient Gough
// Copyright 2017 Michal Witkowski
// All Rights Reserved.
// See LICENSE for licensing terms.

package proxy

import (
	"context"
	"io"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

var (
	clientStreamDescForProxying = &grpc.StreamDesc{
		ServerStreams: true,
		ClientStreams: true,
	}
)

// RegisterService sets up a proxy handler for a particular gRPC service and method.
// The behavior is the same as if you were registering a handler method, e.g. from a codegenerated pb.go file.
//
// This can *only* be used if the `server` also uses proxy.CodecForServer() ServerOption.
func RegisterService(server *grpc.Server, director StreamDirector, serviceName string, methodNames ...string) {
	streamer := &handler{director}
	fakeDesc := &grpc.ServiceDesc{
		ServiceName: serviceName,
		HandlerType: (*interface{})(nil),
	}
	for _, m := range methodNames {
		streamDesc := grpc.StreamDesc{
			StreamName:    m,
			Handler:       streamer.handler,
			ServerStreams: true,
			ClientStreams: true,
		}
		fakeDesc.Streams = append(fakeDesc.Streams, streamDesc)
	}
	server.RegisterService(fakeDesc, streamer)
}

// TransparentHandler returns a handler that attempts to proxy all requests that are not registered in the server.
// The indented use here is as a transparent proxy, where the server doesn't know about the services implemented by the
// backends. It should be used as a `grpc.UnknownServiceHandler`.
//
// This can *only* be used if the `server` also uses proxy.CodecForServer() ServerOption.
func TransparentHandler(director StreamDirector) grpc.StreamHandler {
	streamer := &handler{director}
	return streamer.handler
}

type handler struct {
	director StreamDirector
}

// handler is where the real magic of proxying happens.
// It is invoked like any gRPC server stream and uses the gRPC server framing to get and receive bytes from the wire,
// forwarding it to a ClientStream established against the relevant ClientConn.
func (h *handler) handler(srv interface{}, serverStream grpc.ServerStream) error {
	serverCtx := serverStream.Context()
	ss := grpc.ServerTransportStreamFromContext(serverCtx)
	fullMethodName := ss.Method()
	clientCtx, clientCancel, dir, err := h.director(serverCtx, fullMethodName)
	if err != nil {
		return err
	}
	if clientCancel == nil {
		clientCtx, clientCancel = context.WithCancel(clientCtx)
	}
	defer clientCancel()
	if _, ok := metadata.FromOutgoingContext(clientCtx); !ok {
		clientCtx = CopyMetadata(clientCtx, serverCtx)
	}
	if len(dir.Method) != 0 {
		fullMethodName = dir.Method
	}
	clientStream, err := grpc.NewClientStream(clientCtx, clientStreamDescForProxying, dir.BackendConn, fullMethodName)
	if err != nil {
		return err
	}

	err = biDirCopy(serverStream, clientStream)
	if err == io.EOF {
		err = nil
	}
	if dir.Done != nil {
		dir.Done(err)
	}
	return err
}

const XForwardedFor = "X-Forwarded-For"

// copyMetadata takes the new client (outgoing) context, a server (incoming)
// context, and returns a new outgoing context which contains all the incoming
// metadata.
//
// An additional X-Forwarded-For metadata entry is added or appended to with
// the peer address from the server context. See https://en.wikipedia.org/wiki/X-Forwarded-For.
func CopyMetadata(ctx context.Context, serverCtx context.Context) context.Context {
	remoteIp := RemoteIp(serverCtx)
	if md, ok := metadata.FromIncomingContext(serverCtx); ok {
		md := md.Copy()
		if len(remoteIp) != 0 {
			md.Append(XForwardedFor, remoteIp)
		}
		return metadata.NewOutgoingContext(ctx, md)
	}
	if len(remoteIp) == 0 {
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, metadata.Pairs(XForwardedFor, remoteIp))
}

func RemoteIp(ctx context.Context) string {
	pr, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	addr := pr.Addr
	if addr, ok := addr.(*net.TCPAddr); ok {
		return addr.IP.String()
	}
	s := addr.String()
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s
	}
	return s[:i]
}
