package blazeSDK

import (
	"encoding/binary"
	"math"
)

func (e *Encoder) top() map[string]interface{} { return e.stack[len(e.stack)-1] }

func (e *Encoder) Build() []byte {
	return EncodePacket(&Packet{Header: e.header, Payload: e.stack[0]})
}

func (e *Encoder) Integer(tag string, value int64) *Encoder { e.top()[tag] = value; return e }
func (e *Encoder) String(tag, value string) *Encoder        { e.top()[tag] = value; return e }
func (e *Encoder) Bool(tag string, value bool) *Encoder     { e.top()[tag] = value; return e }
func (e *Encoder) Blob(tag string, value []byte) *Encoder   { e.top()[tag] = value; return e }
func (e *Encoder) Float(tag string, value float32) *Encoder { e.top()[tag] = value; return e }
func (e *Encoder) MsgNum(msgNum uint32) *Encoder            { e.header.MessageNumber = msgNum; return e }
func (e *Encoder) UserIndex(userIndex uint8) *Encoder       { e.header.UserIndex = userIndex; return e }

func (e *Encoder) ObjectType(tag string, component, typ uint16) *Encoder {
	e.top()[tag] = ObjectType{Component: component, Type: typ}
	return e
}

func (e *Encoder) ObjectID(tag string, component, typ uint16, id int64) *Encoder {
	e.top()[tag] = ObjectID{Component: component, Type: typ, ID: id}
	return e
}

func (e *Encoder) Union(tag string, activeMember uint8, field string, value interface{}) *Encoder {
	e.top()[tag] = Union{ActiveMember: activeMember, Field: field, Value: value}
	return e
}

func (e *Encoder) Variable(tag string, tdfID uint32, field string, value interface{}) *Encoder {
	e.top()[tag] = Variable{TdfID: tdfID, Field: field, Value: value}
	return e
}

func (e *Encoder) Raw(tag string, value interface{}) *Encoder { e.top()[tag] = value; return e }

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
	for i, elem := range elems {
		out[i] = coerce(elemType, elem)
	}
	e.top()[tag] = out
	return e
}
func (e *Encoder) Map(tag string, keyType, valType byte, entries [][2]interface{}) *Encoder {
	outMap := make(map[interface{}]interface{}, len(entries))
	for _, entry := range entries {
		outMap[coerce(keyType, entry[0])] = coerce(valType, entry[1])
	}
	e.top()[tag] = outMap
	return e
}

func encodeStruct(fields map[string]interface{}, root bool) []byte {
	var out []byte
	for _, tag := range sortedKeys(fields) {
		if isEmptyCollection(fields[tag]) {
			continue
		} // EA omits empties
		typ, body := encodeValue(fields[tag])
		out = append(out, encodeTag(tag)...)
		out = append(out, typ)
		out = append(out, body...)
	}
	if !root {
		out = append(out, 0x00)
	}
	return out
}

func encodeValue(value interface{}) (byte, []byte) {
	switch x := value.(type) {
	case int64:
		return 0, EncodeVarsizeInteger(x) // INTEGER

	case bool: // convenience
		if x {
			return 0, EncodeVarsizeInteger(1)
		}
		return 0, EncodeVarsizeInteger(0)

	case string:
		body := EncodeVarsizeInteger(int64(len(x) + 1)) // len incl NUL
		body = append(body, x...)
		body = append(body, 0x00)
		return 1, body

	case []byte:
		return 2, append(EncodeVarsizeInteger(int64(len(x))), x...)

	case map[string]interface{}:
		return 3, encodeStruct(x, false) // nested → terminator

	case ArmedStruct: // polymorphic struct-list element: [arm][members][0x00]
		return 3, append([]byte{x.Arm}, encodeStruct(x.Fields, false)...)

	case []interface{}:
		elemType, _ := encodeValue(x[0]) // homogeneous
		body := []byte{elemType}
		body = append(body, EncodeVarsizeInteger(int64(len(x)))...)
		for _, elem := range x {
			_, elemBody := encodeValue(elem)
			body = append(body, elemBody...)
		}
		return 4, body

	case TypedList: // list with an explicit element type — encodes even when empty
		body := []byte{x.ElemType}
		body = append(body, EncodeVarsizeInteger(int64(len(x.Items)))...)
		for _, elem := range x.Items {
			_, elemBody := encodeValue(elem)
			body = append(body, elemBody...)
		}
		return 4, body

	case map[interface{}]interface{}:
		keys := sortedMapKeys(x)
		keyType, _ := encodeValue(keys[0])
		valType := leafType(x[keys[0]]) // EA: map valType = leaf element type
		body := []byte{keyType, valType}
		body = append(body, EncodeVarsizeInteger(int64(len(x)))...)
		for _, key := range keys {
			_, keyBody := encodeValue(key)
			body = append(body, keyBody...)
			_, valBody := encodeValue(x[key])
			body = append(body, valBody...)
		}
		return 5, body

	case float32:
		bits := math.Float32bits(x)
		return 10, []byte{byte(bits >> 24), byte(bits >> 16), byte(bits >> 8), byte(bits)}

	case ObjectType:
		body := EncodeVarsizeInteger(int64(x.Component))
		return 8, append(body, EncodeVarsizeInteger(int64(x.Type))...)

	case ObjectID:
		body := EncodeVarsizeInteger(int64(x.Component))
		body = append(body, EncodeVarsizeInteger(int64(x.Type))...)
		return 9, append(body, EncodeVarsizeInteger(x.ID)...)

	case Union:
		body := []byte{x.ActiveMember}
		if x.ActiveMember != 0xff && x.ActiveMember != 0x7f {
			body = append(body, encodeTag(x.Field)...)
			typ, valBody := encodeValue(x.Value)
			body = append(body, typ)
			body = append(body, valBody...)
		}
		return 6, body

	case Variable:
		if x.Value == nil {
			return 7, []byte{0x00}
		} // not present
		body := []byte{0x01}
		body = append(body, EncodeVarsizeInteger(int64(x.TdfID))...)
		body = append(body, encodeTag(x.Field)...)
		typ, valBody := encodeValue(x.Value)
		body = append(body, typ)
		body = append(body, valBody...)
		return 7, append(body, 0x00) // trailing terminator
	}
	return 0xff, nil
}

func typeByte(value interface{}) byte {
	switch value.(type) {
	case int64, bool:
		return 0
	case string:
		return 1
	case []byte:
		return 2
	case map[string]interface{}, ArmedStruct:
		return 3
	case []interface{}, TypedList:
		return 4
	case map[interface{}]interface{}:
		return 5
	case Union:
		return 6
	case Variable:
		return 7
	case ObjectType:
		return 8
	case ObjectID:
		return 9
	case float32:
		return 10
	}
	return 0xff
}

func leafType(value interface{}) byte {
	switch x := value.(type) {
	case []interface{}:
		if len(x) > 0 {
			return leafType(x[0])
		}
		return 3
	case map[interface{}]interface{}:
		for _, v := range x {
			return leafType(v)
		}
		return 3
	}
	return typeByte(value)
}

func encodeTag(tag string) []byte {
	var packed uint32
	for i := 0; i < len(tag) && i < 4; i++ {
		char := tag[i]
		if char >= 'a' && char <= 'z' {
			char -= 0x20
		} // uppercase
		packed |= uint32((char-0x20)&0x3f) << (26 - 6*i)
	}
	return []byte{byte(packed >> 24), byte(packed >> 16), byte(packed >> 8)}
}

func BuildMetadata(cntx, errc int64) []byte {
	out := append(encodeTag("CNTX"), 0) // INTEGER
	out = append(out, EncodeVarsizeInteger(cntx)...)
	out = append(out, encodeTag("ERRC")...)
	out = append(out, 0)
	out = append(out, EncodeVarsizeInteger(errc)...)
	return out
}

func EncodeVarsizeInteger(value int64) []byte {
	negative := value < 0
	remaining := uint64(value)
	if negative {
		remaining = uint64(-value)
	}
	firstByte := byte(remaining & 0x3f)
	remaining >>= 6
	if negative {
		firstByte |= 0x40
	}
	if remaining > 0 {
		firstByte |= 0x80
	}
	out := []byte{firstByte}
	for remaining > 0 {
		nextByte := byte(remaining & 0x7f)
		remaining >>= 7
		if remaining > 0 {
			nextByte |= 0x80
		}
		out = append(out, nextByte)
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

func isEmptyCollection(value interface{}) bool {
	switch x := value.(type) {
	case []interface{}:
		return len(x) == 0
	case map[interface{}]interface{}:
		return len(x) == 0
	default:
		return false
	}
}

func coerce(typeCode byte, value interface{}) interface{} {
	switch typeCode {
	case 0, 11:
		switch n := value.(type) {
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
	return value // string, []byte
}
