// Package grpcutil provides a custom gRPC codec that works with protow
// message types (which implement Encode() []byte). This avoids requiring
// protoc-generated proto.Message implementations while still producing
// valid protobuf wire-format bytes on the wire.
package grpcutil

import (
	"fmt"
	"google.golang.org/grpc/encoding"
)

// Name is the codec name registered with gRPC.
const Name = "proto"

func init() {
	encoding.RegisterCodec(rawCodec{})
}

// Encode is the interface that protow message types satisfy.
type Encode interface {
	Encode() []byte
}

type rawCodec struct{}

// Codec is exposed so server code can pass grpc.ForceCodec(grpcutil.Codec{}).
var Codec rawCodec

func (rawCodec) Name() string { return Name }

func (rawCodec) Marshal(v any) ([]byte, error) {
	switch v := v.(type) {
	case []byte:
		return v, nil
	case Encode:
		return v.Encode(), nil
	default:
		return nil, fmt.Errorf("grpcutil: cannot marshal %T", v)
	}
}

func (rawCodec) Unmarshal(data []byte, v any) error {
	switch p := v.(type) {
	case *[]byte:
		*p = data
		return nil
	default:
		return fmt.Errorf("grpcutil: cannot unmarshal into %T", v)
	}
}
