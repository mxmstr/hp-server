package blazeSDK

import (
	"encoding/binary"
	"math"
)

func (e *Encoder) top() map[string]interface{} { return e.stack[len(e.stack)-1] }

func (e *Encoder) Build() []byte {
	return EncodePacket(&Packet{Header: e.header, Payload: e.stack[0]})
}

func (e *Encoder) Integer(tag string, v int64) *Encoder { e.top()[tag] = v; return e }
func (e *Encoder) String(tag, v string) *Encoder        { e.top()[tag] = v; return e }
func (e *Encoder) Bool(tag string, v bool) *Encoder     { e.top()[tag] = v; return e }
func (e *Encoder) Blob(tag string, v []byte) *Encoder   { e.top()[tag] = v; return e }
func (e *Encoder) Float(tag string, v float32) *Encoder { e.top()[tag] = v; return e }
func (e *Encoder) MsgNum(n uint32) *Encoder             { e.header.MessageNumber = n; return e }
func (e *Encoder) UserIndex(i uint8) *Encoder           { e.header.UserIndex = i; return e }

func (e *Encoder) BeginStruct(tag string) *Encoder {
	child := map[string]interface{}{}
	e.top()[tag] = child             // attach to parent now (it's a reference)
	e.stack = append(e.stack, child) // descend
	return e
}

func (e *Encoder) EndStruct() *Encoder {
	if len(e.stack) > 1 {
		e.stack = e.stack[:len(e.stack)-1]
	}
	return e
}

func (e *Encoder) List(tag string, elemType byte, elems ...interface{}) *Encoder {
	out := make([]interface{}, len(elems))
	for i, el := range elems {
		out[i] = coerce(elemType, el)
	}
	e.top()[tag] = out
	return e
}
func (e *Encoder) Map(tag string, keyType, valType byte, entries [][2]interface{}) *Encoder {
	m := make(map[interface{}]interface{}, len(entries))
	for _, kv := range entries {
		m[coerce(keyType, kv[0])] = coerce(valType, kv[1])
	}
	e.top()[tag] = m
	return e
}

func encodeStruct(m map[string]interface{}, root bool) []byte {
	var out []byte
	for _, tag := range sortedKeys(m) {
		if isEmptyCollection(m[tag]) {
			continue
		} // EA omits empties
		typ, body := encodeValue(m[tag])
		out = append(out, encodeTag(tag)...)
		out = append(out, typ)
		out = append(out, body...)
	}
	if !root {
		out = append(out, 0x00)
	}
	return out
}

func encodeValue(v interface{}) (byte, []byte) {
	switch x := v.(type) {
	case int64:
		return 0, EncodeVarsizeInteger(x) // INTEGER

	case bool: // convenience
		if x {
			return 0, EncodeVarsizeInteger(1)
		}
		return 0, EncodeVarsizeInteger(0)

	case string:
		b := EncodeVarsizeInteger(int64(len(x) + 1)) // len incl NUL
		b = append(b, x...)
		b = append(b, 0x00)
		return 1, b

	case []byte:
		return 2, append(EncodeVarsizeInteger(int64(len(x))), x...)

	case map[string]interface{}:
		return 3, encodeStruct(x, false) // nested → terminator

	case []interface{}:
		et, _ := encodeValue(x[0]) // homogeneous
		body := []byte{et}
		body = append(body, EncodeVarsizeInteger(int64(len(x)))...)
		for _, e := range x {
			_, eb := encodeValue(e)
			body = append(body, eb...)
		}
		return 4, body

	case map[interface{}]interface{}:
		keys := sortedMapKeys(x)
		kt, _ := encodeValue(keys[0])
		vt, _ := encodeValue(x[keys[0]])
		body := []byte{kt, vt}
		body = append(body, EncodeVarsizeInteger(int64(len(x)))...)
		for _, k := range keys {
			_, kb := encodeValue(k)
			body = append(body, kb...)
			_, vb := encodeValue(x[k])
			body = append(body, vb...)
		}
		return 5, body

	case float32:
		bits := math.Float32bits(x)
		return 10, []byte{byte(bits >> 24), byte(bits >> 16), byte(bits >> 8), byte(bits)}

	case ObjectType:
		b := EncodeVarsizeInteger(int64(x.Component))
		return 8, append(b, EncodeVarsizeInteger(int64(x.Type))...)

	case ObjectID:
		b := EncodeVarsizeInteger(int64(x.Component))
		b = append(b, EncodeVarsizeInteger(int64(x.Type))...)
		return 9, append(b, EncodeVarsizeInteger(x.ID)...)

	case Union:
		body := []byte{x.ActiveMember}
		if x.ActiveMember != 0xff {
			body = append(body, encodeTag(x.Field)...)
			t, vb := encodeValue(x.Value)
			body = append(body, t)
			body = append(body, vb...)
		}
		return 6, body

	case Variable:
		if x.Value == nil {
			return 7, []byte{0x00}
		} // not present
		body := []byte{0x01}
		body = append(body, EncodeVarsizeInteger(int64(x.TdfID))...)
		body = append(body, encodeTag(x.Field)...)
		t, vb := encodeValue(x.Value)
		body = append(body, t)
		body = append(body, vb...)
		return 7, append(body, 0x00) // trailing terminator
	}
	return 0xff, nil
}

func encodeTag(tag string) []byte {
	var t uint32
	for i := 0; i < len(tag) && i < 4; i++ {
		c := tag[i]
		if c >= 'a' && c <= 'z' {
			c -= 0x20
		} // uppercase
		t |= uint32((c-0x20)&0x3f) << (26 - 6*i)
	}
	return []byte{byte(t >> 24), byte(t >> 16), byte(t >> 8)}
}

func EncodeVarsizeInteger(v int64) []byte {
	neg := v < 0
	u := uint64(v)
	if neg {
		u = uint64(-v)
	}
	first := byte(u & 0x3f)
	u >>= 6
	if neg {
		first |= 0x40
	}
	if u > 0 {
		first |= 0x80
	}
	out := []byte{first}
	for u > 0 {
		b := byte(u & 0x7f)
		u >>= 7
		if u > 0 {
			b |= 0x80
		}
		out = append(out, b)
	}
	return out
}

func EncodePacket(pkt *Packet) []byte {
	payload := encodeStruct(pkt.Payload, true) // root: no terminator

	var metadata []byte
	if pkt.Metadata != nil {
		metadata = encodeStruct(pkt.Metadata, true)
	}

	header := make([]byte, 16)
	binary.BigEndian.PutUint32(header[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint16(header[4:6], uint16(len(metadata)))
	binary.BigEndian.PutUint16(header[6:8], pkt.Header.ComponentID)
	binary.BigEndian.PutUint16(header[8:10], pkt.Header.CommandID)
	header[10] = byte(pkt.Header.MessageNumber >> 16)
	header[11] = byte(pkt.Header.MessageNumber >> 8)
	header[12] = byte(pkt.Header.MessageNumber)
	header[13] = (pkt.Header.MessageType << 5) | (pkt.Header.UserIndex & 0x1f)
	header[14] = pkt.Header.Options
	// header[15] stays 0 (reserved)

	out := make([]byte, 0, 16+len(metadata)+len(payload))
	out = append(out, header...)
	out = append(out, metadata...) // header, then metadata, then payload
	out = append(out, payload...)
	return out
}

func isEmptyCollection(v interface{}) bool {
	switch x := v.(type) {
	case []interface{}:
		return len(x) == 0
	case map[interface{}]interface{}:
		return len(x) == 0
	default:
		return false
	}
}

func coerce(typeCode byte, v interface{}) interface{} {
	switch typeCode {
	case 0, 11:
		switch n := v.(type) {
		case int:
			return int64(n)
		case int64:
			return n
		case uint16:
			return int64(n)
		case uint32:
			return int64(n)
		case bool:
			if n {
				return int64(1)
			}
			return int64(0)
		}
	}
	return v // string, []byte
}
