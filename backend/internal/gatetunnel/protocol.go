package gatetunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/net/websocket"
)

const (
	// frameControl and frameStream keep control messages separate from the
	// byte stream carried by the fixed localhost SSH bridge.
	frameControl byte = 1
	frameStream  byte = 2

	ProtocolVersion = 1
	FrameControl    = frameControl
	FrameStream     = frameStream
)

type controlMessage struct {
	Type      string `json:"type"`
	Protocol  int    `json:"protocol,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ControlMessage and frame helpers are shared by the Linux gate client and
// the cloud gateway. They are intentionally a tiny protocol surface; no
// destination host or arbitrary proxy command is represented here.
type ControlMessage = controlMessage

func SendControl(ws *websocket.Conn, message ControlMessage) error { return sendControl(ws, message) }

func SendStream(ws *websocket.Conn, data []byte) error { return sendStream(ws, data) }

func ReceiveFrame(ws *websocket.Conn) (byte, []byte, error) { return receiveFrame(ws) }

func DecodeControl(data []byte) (ControlMessage, error) { return decodeControl(data) }

func sendControl(ws *websocket.Conn, message controlMessage) error {
	message.Protocol = ProtocolVersion
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	frame := make([]byte, 1+len(data))
	frame[0] = frameControl
	copy(frame[1:], data)
	return websocket.Message.Send(ws, frame)
}

func sendStream(ws *websocket.Conn, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	frame := make([]byte, 1+len(data))
	frame[0] = frameStream
	copy(frame[1:], data)
	return websocket.Message.Send(ws, frame)
}

func receiveFrame(ws *websocket.Conn) (byte, []byte, error) {
	var frame []byte
	if err := websocket.Message.Receive(ws, &frame); err != nil {
		return 0, nil, err
	}
	if len(frame) == 0 {
		return 0, nil, errors.New("empty maintenance frame")
	}
	if frame[0] != frameControl && frame[0] != frameStream {
		return 0, nil, fmt.Errorf("unknown maintenance frame type %d", frame[0])
	}
	return frame[0], frame[1:], nil
}

func decodeControl(data []byte) (controlMessage, error) {
	var message controlMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return controlMessage{}, err
	}
	message.Type = strings.TrimSpace(message.Type)
	if message.Type == "" {
		return controlMessage{}, errors.New("maintenance control message type is required")
	}
	if message.Protocol != 0 && message.Protocol != ProtocolVersion {
		return controlMessage{}, fmt.Errorf("unsupported maintenance protocol %d", message.Protocol)
	}
	return message, nil
}
