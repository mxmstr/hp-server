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
[13]:            message type
[14],[15]:       error value

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

func Fire2Decoder(buffer []byte) int {
	header := buffer[:16]
	buffer = buffer[16:]

	if len(header) != 16 {
		return 0
	}

	payloadSize := header[:4]
	header = header[4:]

	metadataSize := header[:2]
	header = header[2:]

	componentID := header[:2]
	header = header[2:]

	commandID := header[:2]
	header = header[2:]

	messageNum := header[:3]
	header = header[3:]

	messageType := header[:1]
	header = header[1:]

	payloadSizeValue := binary.BigEndian.Uint32(payloadSize)
	metadataSizeValue := binary.BigEndian.Uint16(metadataSize)
	ComponentIDValue := binary.BigEndian.Uint16(componentID)
	CommandIDValue := binary.BigEndian.Uint16(commandID)
	MessageNumValue := uint32(messageNum[0])<<16 | uint32(messageNum[1])<<8 | uint32(messageNum[2]) // hell yeah Uint24
	MessageTypeValue := messageType[0]

	fmt.Println("Payload size: ", payloadSizeValue)
	fmt.Println("Metadata size: ", metadataSizeValue)
	fmt.Println("Component ID: ", ComponentIDValue)
	fmt.Println("Command ID: ", CommandIDValue)
	fmt.Println("Message number: ", MessageNumValue)
	fmt.Println("Message type: ", MessageTypeValue)

	if len(buffer) != 0 {
		fmt.Println("Message contains a payload")

		TDFTag := buffer[:3]
		buffer = buffer[3:]

		TDFType := buffer[:1]
		buffer = buffer[1:]

		TDFLength := buffer[:1]
		buffer = buffer[1:]

		TDFTypeValue := TDFType[0]
		TDFLengthValue := TDFLength[0]

		fmt.Println("TDF tag: ", DecodeTag(TDFTag))
		fmt.Println("TDF type: ", GetTypeByID(int(TDFTypeValue)))
		fmt.Println("TDF length: ", TDFLengthValue)
	}

	return 1
}

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
