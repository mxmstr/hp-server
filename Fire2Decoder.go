package blazeSDK

import (
	"encoding/binary"
	"fmt"
	"math"
)

// DecodeTag - unpacks the ASCII tag name from the 3 tag bytes
func DecodeTag(buffer []byte) string {
	if len(buffer) < 3 {
		return ""
	}

	tag := uint32(buffer[0])<<16 | uint32(buffer[1])<<8 | uint32(buffer[2])

	var buf [4]byte
	size := 4
	for i := 3; i >= 0; i-- {
		sixbits := tag & 0x3f
		if sixbits != 0 {
			buf[i] = byte(sixbits + 32)
		} else {
			buf[i] = 0
			size = i
		}
		tag >>= 6
	}
	return string(buf[:size])
}

// GetTypeByID - map TDF types to their Heat2 type IDs
func GetTypeByID(id int) string {
	switch id {
	case 0:
		return "HEAT_TYPE_INTEGER"
	case 1:
		return "HEAT_TYPE_STRING"
	case 2:
		return "HEAT_TYPE_BLOB"
	case 3:
		return "HEAT_TYPE_TDF"
	case 4:
		return "HEAT_TYPE_LIST"
	case 5:
		return "HEAT_TYPE_MAP"
	case 6:
		return "HEAT_TYPE_UNION"
	case 7:
		return "HEAT_TYPE_VARIABLE"
	case 8:
		return "HEAT_TYPE_OBJECT_TYPE"
	case 9:
		return "HEAT_TYPE_OBJECT_ID"
	case 10:
		return "HEAT_TYPE_FLOAT"
	case 11:
		return "HEAT_TYPE_TIMEVALUE"
	case 12:
		return "HEAT_TYPE_GENERIC_TYPE"
	default:
		return "HEAT_TYPE_INVALID"
	}
}

// PopBuffer - splits n bytes off the front of a buffer returning the bytes and the new buffer
func PopBuffer(buffer []byte, n int) ([]byte, []byte) {
	if len(buffer) < n {
		panic(fmt.Sprintf("PopBuffer(%d): only %d bytes remain", n, len(buffer)))
	}
	return buffer[:n], buffer[n:]
}

// DecodeVarsizeInteger - decodes a Heat2 packed integer to a regular int
func DecodeVarsizeInteger(buffer []byte) (int64, int) {
	if len(buffer) == 0 {
		return 0, 0
	}

	firstByte := buffer[0]
	negative := firstByte&0x40 != 0
	value := uint64(firstByte & 0x3f)
	read := 1

	if firstByte&0x80 != 0 {
		shift := 6
		for read < len(buffer) {
			nextByte := buffer[read]
			value |= uint64(nextByte&0x7f) << shift
			read++
			if nextByte&0x80 == 0 {
				break
			}
			shift += 7
		}
	}

	if negative {
		return -int64(value), read
	}
	return int64(value), read
}

// readValue - main parser for all TDF types
func readValue(typ byte, buf []byte) (interface{}, int) {
	switch GetTypeByID(int(typ)) {

	case "HEAT_TYPE_INTEGER":
		value, numRead := DecodeVarsizeInteger(buf)
		return value, numRead // -> int64

	case "HEAT_TYPE_STRING":
		length, bytesRead := DecodeVarsizeInteger(buf)
		if length < 1 || bytesRead+int(length) > len(buf) {
			return nil, -1
		}
		readString := string(buf[bytesRead : bytesRead+int(length)-1])
		return readString, bytesRead + int(length)

	case "HEAT_TYPE_TDF":
		value, bytesRead := readStruct(buf) // <-- recurse
		return value, bytesRead             // -> map[string]interface{}

	case "HEAT_TYPE_LIST":
		if len(buf) < 1 {
			return nil, -1
		}
		elemType := buf[0]
		count, bytesRead := DecodeVarsizeInteger(buf[1:])
		off := 1 + bytesRead
		list := make([]interface{}, 0, count)
		for i := 0; i < int(count); i++ {
			value, bytesRead2 := readValue(elemType, buf[off:])
			if bytesRead2 < 0 {
				return nil, -1
			}
			off += bytesRead2
			list = append(list, value)
		}
		return list, off

	case "HEAT_TYPE_MAP":
		if len(buf) < 2 {
			return nil, -1
		}
		keyType := buf[0]
		valType := buf[1]
		count, bytesRead := DecodeVarsizeInteger(buf[2:])
		off := 2 + bytesRead
		outMap := make(map[interface{}]interface{}, count)
		for i := 0; i < int(count); i++ {
			value1, bytesRead2 := readValue(keyType, buf[off:])
			if bytesRead2 < 0 {
				return nil, -1
			}
			off += bytesRead2
			value2, bytesRead3 := readValue(valType, buf[off:])
			if bytesRead3 < 0 {
				return nil, -1
			}
			off += bytesRead3
			outMap[value1] = value2
		}
		return outMap, off

	case "HEAT_TYPE_BLOB":
		length, bytesRead := DecodeVarsizeInteger(buf)
		if bytesRead+int(length) > len(buf) {
			return nil, -1
		}
		bytes := make([]byte, length)
		copy(bytes, buf[bytesRead:bytesRead+int(length)])
		return bytes, bytesRead + int(length)

	case "HEAT_TYPE_FLOAT":
		if len(buf) < 4 {
			return nil, -1
		}
		return math.Float32frombits(binary.BigEndian.Uint32(buf[:4])), 4 // -> float32

	case "HEAT_TYPE_TIMEVALUE":
		value, bytesRead := DecodeVarsizeInteger(buf)
		return value, bytesRead // -> int64 (microseconds)

	case "HEAT_TYPE_OBJECT_TYPE":
		component, bytesRead1 := DecodeVarsizeInteger(buf)
		cType, bytesRead2 := DecodeVarsizeInteger(buf[bytesRead1:])
		return ObjectType{uint16(component), uint16(cType)}, bytesRead1 + bytesRead2

	case "HEAT_TYPE_OBJECT_ID":
		component, bytesRead1 := DecodeVarsizeInteger(buf)
		cType, bytesRead2 := DecodeVarsizeInteger(buf[bytesRead1:])
		id, bytesRead3 := DecodeVarsizeInteger(buf[bytesRead1+bytesRead2:])
		return ObjectID{uint16(component), uint16(cType), id}, bytesRead1 + bytesRead2 + bytesRead3

	case "HEAT_TYPE_UNION":
		if len(buf) < 1 {
			return nil, -1
		}
		active := buf[0]
		off := 1
		union := Union{ActiveMember: active}
		if active != 0xff && active != 0x7f { // 0xff / 0x7f == INVALID_MEMBER_INDEX
			tag, value, bytesRead := readElement(buf[off:])
			if bytesRead < 0 {
				return nil, -1
			}
			union.Field, union.Value, off = tag, value, off+bytesRead
		}
		return union, off

	case "HEAT_TYPE_VARIABLE", "HEAT_TYPE_GENERIC_TYPE":
		if len(buf) < 1 {
			return nil, -1
		}
		present := buf[0]
		off := 1
		if present == 0 {
			return Variable{}, off
		}
		tdfId, bytesRead := DecodeVarsizeInteger(buf[off:])
		off += bytesRead
		tag, value, bytesRead2 := readElement(buf[off:])
		if bytesRead2 < 0 {
			return nil, -1
		}
		off += bytesRead2
		if off < len(buf) && buf[off] == 0x00 {
			off++
		} // variable's own trailing terminator
		return Variable{TdfID: uint32(tdfId), Field: tag, Value: value}, off
	}
	return nil, -1
}

// readStruct - reads a TDF struct body until it hits a null terminator or runs out of bytes
func readStruct(buf []byte) (map[string]interface{}, int) {
	out := map[string]interface{}{}
	read := 0
	for read < len(buf) {
		if buf[read] == 0x00 {
			read++
			return out, read
		}
		if len(buf)-read < 4 { // truncated header
			return out, -1
		}
		tag := DecodeTag(buf[read : read+3])
		typ := buf[read+3]
		read += 4
		val, bytesRead := readValue(typ, buf[read:])
		if bytesRead < 0 { // don't advance
			return out, -1
		}
		read += bytesRead
		out[tag] = val
	}
	return out, read
}

// readElement - used to parse UNION, VARIABLE, and GENERIC types
func readElement(buf []byte) (string, interface{}, int) {
	if len(buf) < 4 {
		return "", nil, -1
	}
	tag := DecodeTag(buf[0:3])
	typ := buf[3]
	value, bytesRead := readValue(typ, buf[4:])
	if bytesRead < 0 {
		return "", nil, -1
	}
	return tag, value, 4 + bytesRead
}
