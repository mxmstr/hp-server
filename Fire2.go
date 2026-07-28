package blazeSDK

import (
	"encoding/binary"
	"fmt"
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

// Fire2Decoder - Parses a Fire2 packet and returns a decoded *Packet object
func Fire2Decoder(buffer []byte, debugLog bool) *Packet {
	if len(buffer) < 16 { //
		return nil
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

	var payload map[string]interface{}
	if len(buffer) != 0 {
		var n int
		payload, n = readStruct(buffer)
		if n < 0 {
			payload = nil // some shit happened, return a nil payload instead of a partial payload
		}
	}

	if debugLog {
		fmt.Print("Payload = ")
		writeValue(payload, 0)
		fmt.Println()
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
		Payload:  payload,
		Metadata: metadata,
		Error:    blazeErr,
	}
}

func Fire2Encoder(component, command uint16, msgType uint8) *Encoder {
	return &Encoder{
		header: Header{ComponentID: component, CommandID: command, MessageType: msgType},
		stack:  []map[string]interface{}{{}}, // root payload
	}
}
