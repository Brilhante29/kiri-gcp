// Package protow provides lightweight protobuf wire-format encoding and
// decoding helpers for gRPC message types.  It uses
// google.golang.org/protobuf/encoding/protowire for the low-level encoding
// and presents a friendlier API for the handful of message shapes used in
// kiri's gRPC services (Pub/Sub, Firestore).
//
// The types here deliberately mirror a subset of the real proto-generated
// messages so that real Google Cloud client libraries can marshal/unmarshal
// against them over gRPC.
package protow

import (
	"crypto/rand"
	"encoding/hex"
	"math"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

// Now returns the current UTC time in RFC3339Nano format.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// --- Message type registry ---

// Msg is the interface every protow-built message satisfies.
type Msg interface {
	Fields() []byte // wire-encoded payload
}

// --- Encoder ---

// Encoder builds a proto3 message incrementally.
type Encoder struct {
	buf []byte
}

func NewEncoder(sizeHint int) *Encoder {
	return &Encoder{buf: make([]byte, 0, sizeHint)}
}

func (e *Encoder) Bytes() []byte { return e.buf }

func (e *Encoder) Tag(num int, typ protowire.Type) {
	e.buf = protowire.AppendTag(e.buf, protowire.Number(num), typ)
}

// --- Scalar field writers ---

// Int64, Uint64, Int32, and Bool all write the field tag before the varint
// value, matching String/BytesField/Float64/Float32 below. This was
// previously missing — the value was written with no preceding tag byte,
// producing wire bytes with no field number at all. A spec-compliant
// decoder (including the real Go protobuf runtime used by official Google
// Cloud client libraries) reads the next tag byte in its place, decodes it
// as field number 0, and rejects the message outright (field 0 is reserved
// and always invalid) — confirmed against a real client: ReceivedMessage's
// delivery_attempt field (encoded via Int32) silently made every Pub/Sub
// StreamingPull response unparseable, so the server logged a successful
// send but the official client never delivered the message to the
// caller's handler.
func (e *Encoder) Int64(num int, v int64) {
	e.buf = protowire.AppendTag(e.buf, protowire.Number(num), protowire.VarintType)
	e.buf = protowire.AppendVarint(e.buf, uint64(v))
}
func (e *Encoder) Uint64(num int, v uint64) {
	e.buf = protowire.AppendTag(e.buf, protowire.Number(num), protowire.VarintType)
	e.buf = protowire.AppendVarint(e.buf, v)
}
func (e *Encoder) Int32(num int, v int32) { e.Int64(num, int64(v)) }
func (e *Encoder) Bool(num int, v bool) {
	e.buf = protowire.AppendTag(e.buf, protowire.Number(num), protowire.VarintType)
	if v {
		e.buf = protowire.AppendVarint(e.buf, 1)
	} else {
		e.buf = protowire.AppendVarint(e.buf, 0)
	}
}
func (e *Encoder) String(num int, v string) {
	e.buf = protowire.AppendTag(e.buf, protowire.Number(num), protowire.BytesType)
	e.buf = protowire.AppendString(e.buf, v)
}
func (e *Encoder) BytesField(num int, v []byte) {
	e.buf = protowire.AppendTag(e.buf, protowire.Number(num), protowire.BytesType)
	e.buf = protowire.AppendBytes(e.buf, v)
}
func (e *Encoder) Float64(num int, v float64) {
	e.buf = protowire.AppendTag(e.buf, protowire.Number(num), protowire.Fixed64Type)
	e.buf = protowire.AppendFixed64(e.buf, math.Float64bits(v))
}
func (e *Encoder) Float32(num int, v float32) {
	e.buf = protowire.AppendTag(e.buf, protowire.Number(num), protowire.Fixed32Type)
	e.buf = protowire.AppendFixed32(e.buf, math.Float32bits(v))
}

// AppendMessage encodes a sub-message field.  Caller must have already
// encoded the sub-message body into raw.
func (e *Encoder) AppendMessage(num int, raw []byte) {
	e.buf = protowire.AppendTag(e.buf, protowire.Number(num), protowire.BytesType)
	e.buf = protowire.AppendBytes(e.buf, raw)
}

// MapEntry encodes a single map entry as a sub-message with field 1 = key,
// field 2 = value.  Only string→string is needed for Pub/Sub attributes.
func (e *Encoder) MapEntry(num int, key, value string) {
	inner := NewEncoder(32)
	inner.String(1, key)
	inner.String(2, value)
	e.AppendMessage(num, inner.Bytes())
}

// RepeatedString appends each string as field num, i.e. packed-not-allowed.
func (e *Encoder) RepeatedString(num int, vals []string) {
	for _, v := range vals {
		e.String(num, v)
	}
}

// --- Decoder ---

// Decoder walks over a proto3 payload.
type Decoder struct {
	buf []byte
	pos int
}

func NewDecoder(buf []byte) *Decoder {
	return &Decoder{buf: buf}
}

// Next steps through the buffer one field at a time.  Returns field number,
// wire type, and the raw value bytes.  ok=false means end-of-message.
func (d *Decoder) Next() (num int, typ protowire.Type, val []byte, ok bool) {
	if d.pos >= len(d.buf) {
		return 0, 0, nil, false
	}

	numVal, typ, n := protowire.ConsumeTag(d.buf[d.pos:])
	if n < 0 {
		return 0, 0, nil, false
	}

	d.pos += n
	num = int(numVal)

	switch typ {
	case protowire.VarintType:
		v, n := protowire.ConsumeVarint(d.buf[d.pos:])
		if n < 0 {
			return 0, 0, nil, false
		}
		d.pos += n
		val = protowire.AppendVarint(nil, v)
	case protowire.Fixed64Type:
		v, n := protowire.ConsumeFixed64(d.buf[d.pos:])
		if n < 0 {
			return 0, 0, nil, false
		}
		d.pos += n
		val = protowire.AppendFixed64(nil, v)
	case protowire.Fixed32Type:
		v, n := protowire.ConsumeFixed32(d.buf[d.pos:])
		if n < 0 {
			return 0, 0, nil, false
		}
		d.pos += n
		val = protowire.AppendFixed32(nil, v)
	case protowire.BytesType:
		v, n := protowire.ConsumeBytes(d.buf[d.pos:])
		if n < 0 {
			return 0, 0, nil, false
		}
		d.pos += n
		val = v
	default:
		return 0, 0, nil, false
	}

	return num, typ, val, true
}

// ScanVarint is a convenience: consume a varint field into an int64.
func (d *Decoder) ScanVarint(num int) (int64, bool) {
	for {
		n, typ, val, ok := d.Next()
		if !ok {
			return 0, false
		}
		if n == num && typ == protowire.VarintType {
			v, _ := protowire.ConsumeVarint(val)
			return int64(v), true
		}
	}
}

// ScanString is a convenience: consume a string field.
func (d *Decoder) ScanString(num int) (string, bool) {
	for {
		n, typ, val, ok := d.Next()
		if !ok {
			return "", false
		}
		if n == num && typ == protowire.BytesType {
			return string(val), true
		}
	}
}

// ScanBytes is a convenience: consume a bytes field.
func (d *Decoder) ScanBytes(num int) ([]byte, bool) {
	for {
		n, typ, val, ok := d.Next()
		if !ok {
			return nil, false
		}
		if n == num && typ == protowire.BytesType {
			return val, true
		}
	}
}

// ScanRepeatedStrings collects all string occurrences of a given field number.
func (d *Decoder) ScanRepeatedStrings(num int) []string {
	var out []string
	for {
		n, typ, val, ok := d.Next()
		if !ok {
			return out
		}
		if n == num && typ == protowire.BytesType {
			out = append(out, string(val))
		}
	}
}

// ID returns a random lowercase-hex identifier of n bytes (2n hex chars).
func ID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte(time.Now().UnixNano() >> (i % 8))
		}
	}
	return hex.EncodeToString(b)
}

// --- Pre-built message shapes ---

// StringMsg is a proto3 message with one string field at number 1.
type StringMsg struct {
	Field1 string
}

func (m *StringMsg) Encode() []byte {
	e := NewEncoder(32)
	e.String(1, m.Field1)
	return e.Bytes()
}

func DecodeStringMsg(raw []byte) *StringMsg {
	d := NewDecoder(raw)
	s, _ := d.ScanString(1)
	return &StringMsg{Field1: s}
}
