package hpserver

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/local/reorigin-hotpursuit/legacyfire"
)

type Handler func(context.Context, *Session, *legacyfire.Frame) (*legacyfire.Frame, error)

type Server struct {
	Logger         *slog.Logger
	Handlers       map[uint32]Handler
	AutologBase    string
	mu             sync.Mutex
	nextUserID     int64
	nextSessionID  int64
	nextGameID     int64
	nextMatchID    int64
	users          map[int64]UserIdentity
	sessions       map[int64]*Session
	activePersonas map[int64]*Session
	games          map[int64]*Game
	matchQueue     []*matchRequest
}

// UserIdentity is stable across Blaze connections. Account and persona are
// separate namespaces even when the local allocator gives them the same
// numeric value; Session.SessionID is allocated independently per connection.
type UserIdentity struct {
	AccountID int64
	PersonaID int64
	Name      string
}

type Session struct {
	RemoteAddr             net.Addr
	AccountID              int64
	PersonaID              int64
	SessionID              int64
	Persona                string
	Notifications          []*legacyfire.Frame
	NotificationDelay      time.Duration
	PendingMatchmakingGame int64
	NetworkAddress         map[string]interface{}
	NetworkQOS             map[string]interface{}
	GameID                 int64
	QueuedMatchID          int64
	outbound               chan outboundFrame
	done                   chan struct{}
	closeOnce              sync.Once
	mu                     sync.Mutex
}

type outboundFrame struct {
	frame *legacyfire.Frame
	delay time.Duration
}

type Game struct {
	ID           int64
	Host         *Session
	Members      []*Session
	Attrs        map[interface{}]interface{}
	Capacity     int64
	Topology     int64
	State        int64
	Settings     int64
	Slots        map[int64]int64
	JoinedAt     map[int64]int64
	PlayerAttrs  map[int64]map[interface{}]interface{}
	PlayerStates map[int64]int64
	Mesh         map[int64]map[int64]int64
	MeshReady    map[int64]bool
}

type matchRequest struct {
	session  *Session
	attrs    map[interface{}]interface{}
	cap      int64
	topology int64
	msid     int64
}
