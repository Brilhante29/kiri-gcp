// Package protow provides lightweight protobuf wire-format encoding and
// decoding helpers for gRPC message types.
//
// This file holds message types and marshaling routines specific to the
// Google Pub/Sub and Firestore gRPC APIs.
package protow

import "google.golang.org/protobuf/encoding/protowire"

// --- Pub/Sub message types ---

// PubsubMessage is a minimal Pub/Sub message for gRPC.
// Fields (proto3): 1=data (bytes), 2=attributes (map<string,string>), 3=messageId (string)
type PubsubMessage struct {
	Data       []byte
	Attributes map[string]string
	MessageId  string
}

func (m *PubsubMessage) Encode() []byte {
	e := NewEncoder(64)
	if len(m.Data) > 0 {
		e.BytesField(1, m.Data)
	}
	for k, v := range m.Attributes {
		e.MapEntry(2, k, v)
	}
	if m.MessageId != "" {
		e.String(3, m.MessageId)
	}
	return e.Bytes()
}

func DecodePubsubMessage(raw []byte) *PubsubMessage {
	m := &PubsubMessage{Attributes: map[string]string{}}
	d := NewDecoder(raw)
	for {
		num, typ, val, ok := d.Next()
		if !ok {
			break
		}
		switch {
		case num == 1 && typ == protowire.BytesType:
			m.Data = val
		case num == 2 && typ == protowire.BytesType:
			entry := NewDecoder(val)
			var key, value string
			for {
				n2, t2, v2, ok2 := entry.Next()
				if !ok2 {
					break
				}
				if t2 != protowire.BytesType {
					continue
				}
				switch n2 {
				case 1:
					key = string(v2)
				case 2:
					value = string(v2)
				}
			}
			m.Attributes[key] = value
		case num == 3 && typ == protowire.BytesType:
			m.MessageId = string(val)
		}
	}
	return m
}

// PublishRequest: 1=topic (string), 2=messages (repeated PubsubMessage)
type PublishRequest struct {
	Topic    string
	Messages []*PubsubMessage
}

func (m *PublishRequest) Encode() []byte {
	e := NewEncoder(128)
	e.String(1, m.Topic)
	for _, msg := range m.Messages {
		e.AppendMessage(2, msg.Encode())
	}
	return e.Bytes()
}

func DecodePublishRequest(raw []byte) *PublishRequest {
	m := &PublishRequest{}
	d := NewDecoder(raw)
	for {
		num, typ, val, ok := d.Next()
		if !ok {
			break
		}
		if num == 1 && typ == protowire.BytesType {
			m.Topic = string(val)
		} else if num == 2 && typ == protowire.BytesType {
			m.Messages = append(m.Messages, DecodePubsubMessage(val))
		}
	}
	return m
}

// PublishResponse: 1=messageIds (repeated string)
type PublishResponse struct {
	MessageIds []string
}

func (m *PublishResponse) Encode() []byte {
	e := NewEncoder(64)
	e.RepeatedString(1, m.MessageIds)
	return e.Bytes()
}

// PullRequest: 1=subscription (string), 2=maxMessages (int32), 3=returnImmediately (bool)
type PullRequest struct {
	Subscription       string
	MaxMessages        int32
	ReturnImmediately  bool
}

func (m *PullRequest) Encode() []byte {
	e := NewEncoder(32)
	e.String(1, m.Subscription)
	e.Int32(2, m.MaxMessages)
	e.Bool(3, m.ReturnImmediately)
	return e.Bytes()
}

func DecodePullRequest(raw []byte) *PullRequest {
	m := &PullRequest{}
	d := NewDecoder(raw)
	for {
		num, typ, val, ok := d.Next()
		if !ok {
			break
		}
		switch {
		case num == 1 && typ == protowire.BytesType:
			m.Subscription = string(val)
		case num == 2 && typ == protowire.VarintType:
			v, _ := protowire.ConsumeVarint(val)
			m.MaxMessages = int32(v)
		case num == 3 && typ == protowire.VarintType:
			v, _ := protowire.ConsumeVarint(val)
			m.ReturnImmediately = v != 0
		}
	}
	return m
}

// ReceivedMessage: 1=ackId (string), 2=message (PubsubMessage), 3=deliveryAttempt (int32)
type ReceivedMessage struct {
	AckId    string
	Message  *PubsubMessage
	Delivery int32
}

func (m *ReceivedMessage) Encode() []byte {
	e := NewEncoder(64)
	e.String(1, m.AckId)
	if m.Message != nil {
		e.AppendMessage(2, m.Message.Encode())
	}
	e.Int32(3, m.Delivery)
	return e.Bytes()
}

// PullResponse: 1=receivedMessages (repeated ReceivedMessage)
type PullResponse struct {
	Messages []*ReceivedMessage
}

func (m *PullResponse) Encode() []byte {
	e := NewEncoder(128)
	for _, rm := range m.Messages {
		e.AppendMessage(1, rm.Encode())
	}
	return e.Bytes()
}

// AcknowledgeRequest: 1=subscription (string), 2=ackIds (repeated string)
type AcknowledgeRequest struct {
	Subscription string
	AckIds       []string
}

func (m *AcknowledgeRequest) Encode() []byte {
	e := NewEncoder(64)
	e.String(1, m.Subscription)
	e.RepeatedString(2, m.AckIds)
	return e.Bytes()
}

func DecodeAcknowledgeRequest(raw []byte) *AcknowledgeRequest {
	m := &AcknowledgeRequest{}
	d := NewDecoder(raw)
	for {
		num, typ, val, ok := d.Next()
		if !ok {
			break
		}
		if num == 1 && typ == protowire.BytesType {
			m.Subscription = string(val)
		} else if num == 2 && typ == protowire.BytesType {
			m.AckIds = append(m.AckIds, string(val))
		}
	}
	return m
}

// StreamingPullRequest (minimal): 1=subscription (string, set on the first
// request only), 2=ackIds (repeated string). Real clients (including
// cloud.google.com/go/pubsub) also send modifyDeadlineSeconds/ackDeadline/
// clientId fields, which this emulator does not model — it acks on demand
// instead of tracking per-message deadlines.
type StreamingPullRequest struct {
	Subscription string
	AckIds       []string
}

func DecodeStreamingPullRequest(raw []byte) *StreamingPullRequest {
	m := &StreamingPullRequest{}
	d := NewDecoder(raw)

	for {
		num, typ, val, ok := d.Next()
		if !ok {
			break
		}

		if num == 1 && typ == protowire.BytesType {
			m.Subscription = string(val)
		} else if num == 2 && typ == protowire.BytesType {
			m.AckIds = append(m.AckIds, string(val))
		}
	}

	return m
}

// StreamingPullResponse: 1=receivedMessages (repeated ReceivedMessage) — the
// same field shape as PullResponse, so PullResponse.Encode() is reused
// directly as the wire encoder for streaming responses too.

// Subscription proto (minimal): 1=name (string), 2=topic (string)
type SubscriptionMsg struct {
	Name  string
	Topic string
}

func (m *SubscriptionMsg) Encode() []byte {
	e := NewEncoder(32)
	e.String(1, m.Name)
	e.String(2, m.Topic)
	return e.Bytes()
}

// Topic proto (minimal): 1=name (string)
type TopicMsg struct {
	Name string
}

func (m *TopicMsg) Encode() []byte {
	e := NewEncoder(32)
	e.String(1, m.Name)
	return e.Bytes()
}

// ListTopicsRequest: 1=parent (string), 2=pageSize (int32)
// ListTopicsResponse: 1=topics (repeated TopicMsg)
type ListTopicsResponse struct {
	Topics []*TopicMsg
}

func (m *ListTopicsResponse) Encode() []byte {
	e := NewEncoder(64)
	for _, t := range m.Topics {
		e.AppendMessage(1, t.Encode())
	}
	return e.Bytes()
}

// ListSubscriptionsRequest: 1=parent (string)
// ListSubscriptionsResponse: 1=subscriptions (repeated SubscriptionMsg)
type ListSubscriptionsResponse struct {
	Subscriptions []*SubscriptionMsg
}

func (m *ListSubscriptionsResponse) Encode() []byte {
	e := NewEncoder(64)
	for _, s := range m.Subscriptions {
		e.AppendMessage(1, s.Encode())
	}
	return e.Bytes()
}

// Empty is a proto3 empty message.
type Empty struct{}

func (m *Empty) Encode() []byte { return nil }

// --- Firestore message types ---

// Document proto (minimal): 1=name (string), 2=fields (struct), 3=createTime, 4=updateTime
type DocumentMsg struct {
	Name       string
	CreateTime string
	UpdateTime string
}

func (m *DocumentMsg) Encode() []byte {
	e := NewEncoder(128)
	e.String(1, m.Name)
	e.String(3, m.CreateTime)
	e.String(4, m.UpdateTime)
	return e.Bytes()
}

// GetDocumentRequest: 1=name (string), 2=transaction (bytes)
type GetDocumentRequest struct {
	Name string
}

func DecodeGetDocumentRequest(raw []byte) *GetDocumentRequest {
	d := NewDecoder(raw)
	name, _ := d.ScanString(1)
	return &GetDocumentRequest{Name: name}
}

// ListDocumentsRequest: 1=parent (string), 2=collectionId (string)
type ListDocumentsRequest struct {
	Parent       string
	CollectionId string
}

func DecodeListDocumentsRequest(raw []byte) *ListDocumentsRequest {
	r := &ListDocumentsRequest{}
	d := NewDecoder(raw)
	for {
		num, typ, val, ok := d.Next()
		if !ok {
			break
		}
		if num == 1 && typ == protowire.BytesType {
			r.Parent = string(val)
		} else if num == 2 && typ == protowire.BytesType {
			r.CollectionId = string(val)
		}
	}
	return r
}

// ListDocumentsResponse: 1=documents (repeated DocumentMsg)
type ListDocumentsResponse struct {
	Documents []*DocumentMsg
}

func (m *ListDocumentsResponse) Encode() []byte {
	e := NewEncoder(128)
	for _, d := range m.Documents {
		e.AppendMessage(1, d.Encode())
	}
	return e.Bytes()
}

// CreateDocumentRequest: 1=parent (string), 2=collectionId (string), 3=documentId (string), 4=document (DocumentMsg)
type CreateDocumentRequest struct {
	Parent       string
	CollectionId string
	DocumentId   string
}

func DecodeCreateDocumentRequest(raw []byte) *CreateDocumentRequest {
	r := &CreateDocumentRequest{}
	d := NewDecoder(raw)
	for {
		num, typ, val, ok := d.Next()
		if !ok {
			break
		}
		if num == 1 && typ == protowire.BytesType {
			r.Parent = string(val)
		} else if num == 2 && typ == protowire.BytesType {
			r.CollectionId = string(val)
		} else if num == 3 && typ == protowire.BytesType {
			r.DocumentId = string(val)
		}
	}
	return r
}
