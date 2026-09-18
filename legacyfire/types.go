package legacyfire

const HeaderSize = 12

const (
	MessageRequest uint16 = 0x0000
	MessageReply   uint16 = 0x1000
	MessageNotify  uint16 = 0x2000
	MessageError   uint16 = 0x3000
)

type Header struct {
	PayloadSize uint16
	Component   uint16
	Command     uint16
	Error       uint16
	MessageType uint16
	MessageID   uint16
}

type Frame struct {
	Header  Header
	Payload []byte
	TDF     map[string]interface{}
}
