package legacyfire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	blaze "github.com/local/reorigin-hotpursuit"
)

func (h Header) MarshalBinary() []byte {

	b := make([]byte, HeaderSize)
	binary.BigEndian.PutUint16(b[0:2], h.PayloadSize)
	binary.BigEndian.PutUint16(b[2:4], h.Component)
	binary.BigEndian.PutUint16(b[4:6], h.Command)
	binary.BigEndian.PutUint16(b[6:8], h.Error)
	binary.BigEndian.PutUint16(b[8:10], h.MessageType)
	binary.BigEndian.PutUint16(b[10:12], h.MessageID)

	return b

}

func ParseHeader(b []byte) (Header, error) {

	if len(b) < HeaderSize {
		return Header{}, io.ErrUnexpectedEOF
	}

	return Header{
		PayloadSize: binary.BigEndian.Uint16(b[0:2]),
		Component:   binary.BigEndian.Uint16(b[2:4]),
		Command:     binary.BigEndian.Uint16(b[4:6]),
		Error:       binary.BigEndian.Uint16(b[6:8]),
		MessageType: binary.BigEndian.Uint16(b[8:10]),
		MessageID:   binary.BigEndian.Uint16(b[10:12]),
	}, nil

}

func Read(r io.Reader) (*Frame, error) {

	headerBytes := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, headerBytes); err != nil {
		return nil, err
	}

	h, err := ParseHeader(headerBytes)
	if err != nil {
		return nil, err
	}

	payload := make([]byte, int(h.PayloadSize))
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}

	f := &Frame{Header: h, Payload: payload}
	if len(payload) != 0 {

		decoded, consumed := blaze.DecodeTDF(payload)
		if consumed >= 0 {
			f.TDF = decoded
		}

	}

	return f, nil

}

func Write(w io.Writer, f *Frame) error {

	if len(f.Payload) > 0xffff {
		return errors.New("legacy FIRE payload exceeds uint16 length")
	}

	f.Header.PayloadSize = uint16(len(f.Payload))
	packet := append(f.Header.MarshalBinary(), f.Payload...)

	for len(packet) != 0 {

		n, err := w.Write(packet)

		if err != nil {
			return err
		}

		if n == 0 {
			return io.ErrShortWrite
		}

		packet = packet[n:]

	}

	return nil

}

func Reply(req *Frame, payload []byte) *Frame {

	return &Frame{Header: Header{Component: req.Header.Component,
		Command: req.Header.Command, MessageType: MessageReply,
		MessageID: req.Header.MessageID}, Payload: payload}

}

func ErrorReply(req *Frame, code uint16, payload []byte) *Frame {

	return &Frame{Header: Header{Component: req.Header.Component,
		Command: req.Header.Command, Error: code, MessageType: MessageError,
		MessageID: req.Header.MessageID}, Payload: payload}

}

func (h Header) String() string {

	return fmt.Sprintf("component=%d command=0x%04x error=0x%04x type=0x%04x id=%d payload=%d",
		h.Component, h.Command, h.Error, h.MessageType, h.MessageID, h.PayloadSize)

}
