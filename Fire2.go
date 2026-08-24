package blazeSDK

import (
	"encoding/binary"
)

/*
fire2 packet header

[0],[1],[2],[3]: payload size
[4],[5]:         metadata size
[6],[7]:         component ID
[8],[9]:         command ID
[10],[11],[12]:  message number
[13]:      message type (<<5) | user index (&0x1f)
[14]:      options (OPTION_IMMEDIATE = 0x01)
[15]:      reserved

fire2 packet payload

[0],[1],[2]: TDF tag
[3]:         TDF type
[4]:         TDF size
[5]...       TDF body

fire2 message types

MESSAGE = 0
REPLY = 1
NOTIFICATION = 2
ERROR_REPLY = 3
PING = 4
PING_REPLY = 5

blaze component IDs

---- generic ----
1: Authentication
3: Example
4: GameManager
5: Redirector
7: Stats
9: Util
10: CensusData
11: Clubs
15: Messaging
25: AssociationLists
27: GpsContentController
28: GameReporting
31: ByteVault
33: Achievements
1025: XBLSystemConfigs
1031: Friends

---- specific ----
802: Packs
803: Inventory
805: PvzGw
7802: UserSessions
*/

func Fire2Decoder(buffer []byte) (*Packet, string) {
	if len(buffer) < 16 { //
		return nil, ""
	}

	var header []byte
	header, buffer = PopBuffer(buffer, 16)

	var payloadSize, metadataSize, componentID, commandID, messageNum, messageType []byte

	payloadSize, header = PopBuffer(header, 4)
	metadataSize, header = PopBuffer(header, 2)
	componentID, header = PopBuffer(header, 2)
	commandID, header = PopBuffer(header, 2)
	messageNum, header = PopBuffer(header, 3)
	messageType, header = PopBuffer(header, 1)
	options, header := PopBuffer(header, 1)

	payloadSizeValue := binary.BigEndian.Uint32(payloadSize)
	metadataSizeValue := binary.BigEndian.Uint16(metadataSize)
	ComponentIDValue := binary.BigEndian.Uint16(componentID)
	CommandIDValue := binary.BigEndian.Uint16(commandID)
	MessageNumValue := uint32(messageNum[0])<<16 | uint32(messageNum[1])<<8 | uint32(messageNum[2]) // hell yeah Uint24
	MessageTypeValue := messageType[0] >> 5
	UserIndexValue := messageType[0] & 0x1f

	var metadata map[string]interface{}
	if metadataSizeValue > 0 {
		metaBytes := buffer[:metadataSizeValue]
		buffer = buffer[metadataSizeValue:]
		metadata, _ = readStruct(metaBytes)
	}

	const MsgErrorReply = 3

	var blazeErr *BlazeError
	errCode := uint32(0)
	if v, ok := metadata["ERRC"].(int64); ok {
		errCode = uint32(v)
	}
	if MessageTypeValue == MsgErrorReply || errCode != 0 {
		blazeErr = &BlazeError{Component: ComponentIDValue, Code: errCode}
	}

	rawPayload := buffer

	var payload map[string]interface{}
	if len(buffer) != 0 {
		var n int
		payload, n = readStruct(buffer)
		if n < 0 {
			payload = nil // some shit happened, return a nil payload instead of a partial payload
		}
	}

	return &Packet{
		Header: Header{
			PayloadSize:   payloadSizeValue,
			MetadataSize:  metadataSizeValue,
			ComponentID:   ComponentIDValue,
			CommandID:     CommandIDValue,
			MessageNumber: MessageNumValue,
			MessageType:   MessageTypeValue,
			UserIndex:     UserIndexValue,
			Options:       options[0],
		},
		Payload:    payload,
		Metadata:   metadata,
		RawPayload: rawPayload,
		Error:      blazeErr,
	}, formatPayload(payload)
}

func FindTagInt(raw []byte, tag string) (int64, bool) {
	t := encodeTag(tag)
	for i := 0; i+4 <= len(raw); i++ {
		if raw[i] == t[0] && raw[i+1] == t[1] && raw[i+2] == t[2] && raw[i+3] == 0 {
			v, n := DecodeVarsizeInteger(raw[i+4:])
			if n > 0 {
				return v, true
			}
		}
	}
	return 0, false
}

func BuildRawFrame(comp, cmd uint16, msgType uint8, msgNum uint32, payload []byte) []byte {
	out := make([]byte, 16+len(payload))
	binary.BigEndian.PutUint32(out[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint16(out[6:8], comp)
	binary.BigEndian.PutUint16(out[8:10], cmd)
	out[10] = byte(msgNum >> 16)
	out[11] = byte(msgNum >> 8)
	out[12] = byte(msgNum)
	out[13] = msgType << 5
	copy(out[16:], payload)
	return out
}

// DecodeTDF exposes the shared TDF decoder for legacy FIRE transports.
func DecodeTDF(buffer []byte) (map[string]interface{}, int) {
	return readStruct(buffer)
}

func Fire2Encoder(component, command uint16, msgType uint8) *Encoder {
	return &Encoder{
		header: Header{ComponentID: component, CommandID: command, MessageType: msgType},
		stack:  []map[string]interface{}{{}}, // root payload
	}
}
