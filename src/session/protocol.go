package session

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// Message types
const (
	MsgData     byte = 0x01 // PTY data (bidirectional)
	MsgResize   byte = 0x02 // Resize request (client -> daemon)
	MsgInfo     byte = 0x03 // Info request (client -> daemon)
	MsgInfoResp byte = 0x04 // Info response (daemon -> client)
	MsgHistory  byte = 0x05 // History complete marker (daemon -> client)
	MsgClose    byte = 0x06 // Close/terminate session (client -> daemon)
)

// Info contains session metadata
type Info struct {
	Name    string `json:"name"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
	SSHHost string `json:"ssh_host,omitempty"`
}

// WriteMessage writes a framed message to the writer
func WriteMessage(w io.Writer, msgType byte, payload []byte) error {
	// Header: type (1) + length (4)
	header := make([]byte, 5)
	header[0] = msgType
	binary.BigEndian.PutUint32(header[1:5], uint32(len(payload)))

	if _, err := w.Write(header); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// ReadMessage reads a framed message from the reader
func ReadMessage(r io.Reader) (msgType byte, payload []byte, err error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}

	msgType = header[0]
	length := binary.BigEndian.Uint32(header[1:5])

	if length > 10*1024*1024 { // 10MB max
		return 0, nil, fmt.Errorf("message too large: %d bytes", length)
	}

	if length > 0 {
		payload = make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, err
		}
	}

	return msgType, payload, nil
}

// WriteData writes a data message
func WriteData(w io.Writer, data []byte) error {
	return WriteMessage(w, MsgData, data)
}

// WriteResize writes a resize message
func WriteResize(w io.Writer, cols, rows uint16) error {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint16(payload[0:2], cols)
	binary.BigEndian.PutUint16(payload[2:4], rows)
	return WriteMessage(w, MsgResize, payload)
}

// ParseResize parses a resize payload
func ParseResize(payload []byte) (cols, rows uint16, err error) {
	if len(payload) != 4 {
		return 0, 0, fmt.Errorf("invalid resize payload length: %d", len(payload))
	}
	cols = binary.BigEndian.Uint16(payload[0:2])
	rows = binary.BigEndian.Uint16(payload[2:4])
	return cols, rows, nil
}

// WriteInfo writes an info response
func WriteInfo(w io.Writer, info Info) error {
	payload, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return WriteMessage(w, MsgInfoResp, payload)
}

// ParseInfo parses an info response payload
func ParseInfo(payload []byte) (Info, error) {
	var info Info
	err := json.Unmarshal(payload, &info)
	return info, err
}

// WriteHistoryComplete writes the history complete marker
func WriteHistoryComplete(w io.Writer) error {
	return WriteMessage(w, MsgHistory, nil)
}
