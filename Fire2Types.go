package blazeSDK

type Packet struct {
	Header     Header
	Payload    map[string]interface{}
	Metadata   map[string]interface{}
	RawPayload []byte // undecoded payload bytes (for fields a generic decode can't reach)
	Error      *BlazeError
}

type Header struct {
	PayloadSize   uint32
	MetadataSize  uint16
	ComponentID   uint16
	CommandID     uint16
	MessageNumber uint32
	MessageType   uint8
	UserIndex     uint8
	Options       uint8
}

type BlazeError struct {
	Component uint16
	Code      uint32
}

type ObjectType struct{ Component, Type uint16 }
type ObjectID struct {
	Component, Type uint16
	ID              int64
}
type Union struct {
	ActiveMember uint8
	Field        string
	Value        interface{}
}
type Variable struct {
	TdfID uint32
	Field string
	Value interface{}
} // also used for GENERIC

type ArmedStruct struct {
	Arm    uint8
	Fields map[string]interface{}
}

type TypedList struct {
	ElemType byte
	Items    []interface{}
}

type Encoder struct {
	header Header
	stack  []map[string]interface{}
}
