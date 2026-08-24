package hpserver

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	blaze "github.com/local/reorigin-hotpursuit"
	"github.com/local/reorigin-hotpursuit/legacyfire"
)

const (
	ComponentAuthentication = 1
	ComponentGameManager    = 4
	ComponentRedirector     = 5
	ComponentUtility        = 9
	ComponentAssociation    = 25
	ComponentGameReporting  = 28
	ComponentUserSessions   = 0x7802

	CommandLogin             = 0x28
	CommandSilentLogin       = 0x32
	CommandLogout            = 0x46
	CommandPostAuth          = 0x08
	CommandUserSettingsLoad  = 0x0a
	CommandSetMetrics        = 0x16
	CommandUpdateNetworkInfo = 0x14
	CommandUpdateHardware    = 0x08
	CommandGetTelemetry      = 0x05
	CommandCreateWalSession  = 0xe6
	CommandGetAccount        = 0x1e
	CommandListEntitlements  = 0x20
	CommandGetLists          = 0x06
	CommandSubmitOfflineGame = 0x02
	CommandAdvanceGameState  = 0x03
	CommandSetGameSettings   = 0x04
	CommandSetPlayerCapacity = 0x05
	CommandSetGameAttrs      = 0x07
	CommandSetPlayerAttrs    = 0x08
	CommandRemovePlayer      = 0x0b
	CommandStartMatchmaking  = 0x0d
	CommandFinalizeGame      = 0x0f
	CommandResetDedicated    = 0x19
	CommandUpdateMesh        = 0x1d

	NotifyExtendedData  = 0x01
	NotifyUserAdded     = 0x02
	NotifyGameSetup     = 0x14
	NotifyGameRemoved   = 0x10
	NotifyPlayerJoining = 0x15
	NotifyPlayerJoined  = 0x1e
	NotifyPlayerRemoved = 0x28
	NotifyPlatformHost  = 0x47
	NotifyGameAttrs     = 0x50
	NotifyPlayerAttrs   = 0x5a
	NotifyGameState     = 0x64
	NotifyGameSettings  = 0x6e
	NotifyGameCapacity  = 0x6f
	NotifyPlayerState   = 0x74

	AuthInvalidPassword = 0x0c
	AuthInvalidToken    = 0x0d
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

func newGame(id int64,
	host *Session,
	members []*Session,
	attrs map[interface{}]interface{},
	capacity, topology,
	state int64) *Game {

	game := &Game{
		ID: id, Host: host, Attrs: attrs, Capacity: capacity, Topology: topology,
		State: state, Settings: 284,
		Slots: make(map[int64]int64), JoinedAt: make(map[int64]int64),
		PlayerAttrs: make(map[int64]map[interface{}]interface{}), PlayerStates: make(map[int64]int64),
		Mesh: make(map[int64]map[int64]int64), MeshReady: make(map[int64]bool),
	}
	for _, member := range members {
		game.addMember(member)
	}
	return game

}

func (game *Game) ensureMembershipState() {

	if game.Slots == nil {
		game.Slots = make(map[int64]int64)
	}
	if game.JoinedAt == nil {
		game.JoinedAt = make(map[int64]int64)
	}
	if game.PlayerAttrs == nil {
		game.PlayerAttrs = make(map[int64]map[interface{}]interface{})
	}
	if game.PlayerStates == nil {
		game.PlayerStates = make(map[int64]int64)
	}
	for index, member := range game.Members {
		if _, ok := game.Slots[member.PersonaID]; !ok {
			game.Slots[member.PersonaID] = int64(index)
		}
		if game.JoinedAt[member.PersonaID] == 0 {
			game.JoinedAt[member.PersonaID] = time.Now().Unix()
		}
		if _, ok := game.PlayerStates[member.PersonaID]; !ok {
			game.PlayerStates[member.PersonaID] = 4 // ACTIVE_CONNECTED
		}
	}

}

func (game *Game) addMember(session *Session) (int64, bool) {

	game.ensureMembershipState()
	for _, member := range game.Members {
		if member == session || member.PersonaID == session.PersonaID {
			return game.Slots[member.PersonaID], false
		}
	}
	if game.Capacity > 0 && int64(len(game.Members)) >= game.Capacity {
		return -1, false
	}
	used := make(map[int64]bool, len(game.Slots))
	for _, slot := range game.Slots {
		used[slot] = true
	}
	slot := int64(0)
	for used[slot] {
		slot++
	}
	game.Members = append(game.Members, session)
	game.Slots[session.PersonaID] = slot
	game.JoinedAt[session.PersonaID] = time.Now().Unix()
	game.PlayerAttrs[session.PersonaID] = make(map[interface{}]interface{})
	game.PlayerStates[session.PersonaID] = 4 // ACTIVE_CONNECTED
	return slot, true

}

func (game *Game) removeMember(session *Session) bool {

	for i, member := range game.Members {

		if member != session {
			continue
		}
		game.Members = append(game.Members[:i], game.Members[i+1:]...)
		delete(game.Slots, session.PersonaID)
		delete(game.JoinedAt, session.PersonaID)
		delete(game.PlayerAttrs, session.PersonaID)
		delete(game.PlayerStates, session.PersonaID)
		delete(game.Mesh, session.PersonaID)
		for _, connections := range game.Mesh {
			delete(connections, session.PersonaID)
		}
		delete(game.MeshReady, session.PersonaID)

		return true
	}

	return false

}

func New(logger *slog.Logger) *Server {

	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{Logger: logger, Handlers: make(map[uint32]Handler), AutologBase: "http://127.0.0.1:18080",
		nextUserID: 1, nextSessionID: 1, nextGameID: 1, nextMatchID: 1,
		users: make(map[int64]UserIdentity), sessions: make(map[int64]*Session),
		activePersonas: make(map[int64]*Session), games: make(map[int64]*Game)}
	s.registerDefaults()

	return s

}

func key(component, command uint16) uint32 { return uint32(component)<<16 | uint32(command) }

func (s *Server) Handle(component, command uint16, handler Handler) {
	s.Handlers[key(component, command)] = handler
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go s.serveConn(ctx, conn)
	}

}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {

	defer conn.Close()
	session := s.newSession(conn.RemoteAddr())
	defer s.removeSession(session)
	writeCtx, cancelWriter := context.WithCancel(ctx)
	defer cancelWriter()
	writeErr := make(chan error, 1)
	go s.writeLoop(writeCtx, conn, session, writeErr)

	s.Logger.Info("client connected", "remote", conn.RemoteAddr())

	if handshaker, ok := conn.(interface{ Handshake() error }); ok {
		if err := handshaker.Handshake(); err != nil {
			s.Logger.Warn("transport handshake failed", "remote", conn.RemoteAddr(), "error", err)
			return
		}
		s.Logger.Info("transport ready; waiting for FIRE request", "remote", conn.RemoteAddr())
	}

	for {
		select {
		case err := <-writeErr:
			if err != nil && !errors.Is(err, net.ErrClosed) {
				s.Logger.Warn("write failed", "remote", conn.RemoteAddr(), "error", err)
			}
			return
		default:
		}
		request, err := legacyfire.Read(conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.Logger.Info("client closed connection", "remote", conn.RemoteAddr(), "phase", "FIRE")
			} else if !errors.Is(err, net.ErrClosed) {
				s.Logger.Warn("read failed", "remote", conn.RemoteAddr(), "error", err)
			}
			return
		}
		s.Logger.Info("request", "remote", conn.RemoteAddr(), "header", request.Header.String(),
			"payload_hex", hex.EncodeToString(request.Payload))
		handler := s.Handlers[key(request.Header.Component, request.Header.Command)]
		if handler == nil {
			s.Logger.Warn("unimplemented request", "header", request.Header.String())
			return
		}
		response, err := handler(ctx, session, request)
		if err != nil {
			s.Logger.Warn("handler failed", "header", request.Header.String(), "error", err)
			return
		}
		if response != nil {
			if !session.enqueue(outboundFrame{frame: response}) {
				return
			}
		}
		session.flushNotifications()
	}

}

func (s *Server) newSession(remote net.Addr) *Session {
	return &Session{RemoteAddr: remote, outbound: make(chan outboundFrame, 256), done: make(chan struct{})}
}

func (s *Server) authenticateSession(session *Session) {
	s.authenticateSessionAs(session, 0)
}

func (s *Server) authenticateSessionAs(session *Session, requestedPersonaID int64) {

	s.mu.Lock()
	defer s.mu.Unlock()
	if session.PersonaID != 0 {
		return
	}
	personaID := requestedPersonaID
	// Treat the persisted PID as a stable preference. Two fresh installations
	// can both request PID 1; in that case provision a brand-new canonical user
	// and return that identity coherently in every authentication/session field.
	if personaID <= 0 || s.activePersonas[personaID] != nil {
		personaID = s.nextUserID
		for s.activePersonas[personaID] != nil || s.users[personaID].PersonaID != 0 {
			personaID++
		}
	}
	identity, exists := s.users[personaID]
	if !exists {
		identity = UserIdentity{AccountID: personaID, PersonaID: personaID, Name: personaName(personaID)}
		s.users[personaID] = identity
	}
	if personaID >= s.nextUserID {
		s.nextUserID = personaID + 1
	}
	session.AccountID = identity.AccountID
	session.PersonaID = identity.PersonaID
	session.SessionID = s.nextSessionID
	s.nextSessionID++
	session.Persona = identity.Name
	s.sessions[session.SessionID] = session
	s.activePersonas[session.PersonaID] = session

}

func personaName(personaID int64) string {
	if personaID <= 1 {
		return "Player"
	}
	return fmt.Sprintf("Player%d", personaID)
}

// userSessionID is the server-wide connection identity used by PROS.UID,
// NotifyExtendedData.USID, GAME.HSES, and matchmaking setup reasons.
func (session *Session) userSessionID() int64 {
	if session.SessionID != 0 {
		return session.SessionID
	}
	if session.PersonaID != 0 {
		return session.PersonaID
	}
	return session.PersonaID
}

func (s *Server) removeSession(session *Session) {

	session.close()
	s.mu.Lock()
	delete(s.sessions, session.SessionID)
	if s.activePersonas[session.PersonaID] == session {
		delete(s.activePersonas, session.PersonaID)
	}
	s.removeQueuedSessionLocked(session)
	gameID := session.GameID
	game := s.games[gameID]
	session.GameID = 0
	session.PendingMatchmakingGame = 0
	session.QueuedMatchID = 0

	if game == nil {
		// Capture the registered membership even if a caller cleared GameID
		// before the connection teardown ran.
		for _, candidate := range s.games {
			if sessionForPlayerID(candidate, session.PersonaID) == session {
				game = candidate
				break
			}
		}
	}

	var remaining []*Session
	hostLeft := false
	if game != nil {
		game.removeMember(session)
		remaining = append(remaining, game.Members...)
		if game.Host == session {
			hostLeft = true
			delete(s.games, game.ID)
			for _, member := range game.Members {
				member.GameID = 0
				member.PendingMatchmakingGame = 0
				member.QueuedMatchID = 0
			}
		} else {
			refreshMeshReadiness(game)
		}
	}

	s.mu.Unlock()
	if game != nil {
		for _, member := range remaining {
			member.notify(playerRemovedNotification(game.ID, session.PersonaID, 0, 1))
			if hostLeft {
				member.notify(gameRemovedNotification(game.ID, 2))
			}
		}
	}

}

// removeQueuedSessionLocked enforces the Session state machine: a connection
// can be idle, queued, or in one game, never present in more than one state.
// The caller must hold Server.mu.
func (s *Server) removeQueuedSessionLocked(session *Session) {
	for i := len(s.matchQueue) - 1; i >= 0; i-- {
		if s.matchQueue[i].session == session {
			s.matchQueue = append(s.matchQueue[:i], s.matchQueue[i+1:]...)
		}
	}
	session.QueuedMatchID = 0
}

func playerRemovedNotification(gameID, playerID, contextValue, reason int64) *legacyfire.Frame {
	return notification(ComponentGameManager, NotifyPlayerRemoved, blaze.EncodeTDF(map[string]interface{}{
		"CNTX": contextValue,
		"GID":  gameID,
		"PID":  playerID,
		"REAS": reason,
	}))
}

func gameRemovedNotification(gameID, reason int64) *legacyfire.Frame {
	return notification(ComponentGameManager, NotifyGameRemoved, blaze.EncodeTDF(map[string]interface{}{
		"GID": gameID, "REAS": reason,
	}))
}

func playerJoinCompletedNotification(gameID, playerID int64) *legacyfire.Frame {
	return notification(ComponentGameManager, NotifyPlayerJoined, blaze.EncodeTDF(map[string]interface{}{
		"GID": gameID, "PID": playerID,
	}))
}

func (s *Server) writeLoop(ctx context.Context, conn net.Conn, session *Session, result chan<- error) {
	for {
		select {
		case <-ctx.Done():
			result <- ctx.Err()
			return
		case item := <-session.outbound:
			if item.delay > 0 {
				select {
				case <-time.After(item.delay):
				case <-ctx.Done():
					result <- ctx.Err()
					return
				}
			}
			if err := legacyfire.Write(conn, item.frame); err != nil {
				_ = conn.Close()
				result <- err
				return
			}
			s.Logger.Info("outbound", "remote", conn.RemoteAddr(), "header", item.frame.Header.String(), "payload_bytes", len(item.frame.Payload))
		}
	}
}

func (session *Session) notify(frame *legacyfire.Frame) {
	if session.outbound != nil {
		session.enqueue(outboundFrame{frame: frame})
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.Notifications = append(session.Notifications, frame)
}

func (session *Session) flushNotifications() {
	session.mu.Lock()
	frames, delay := session.Notifications, session.NotificationDelay
	session.Notifications, session.NotificationDelay = nil, 0
	session.mu.Unlock()
	for i, frame := range frames {
		d := time.Duration(0)
		if i == 0 {
			d = delay
		}
		if !session.enqueue(outboundFrame{frame: frame, delay: d}) {
			return
		}
	}
}

func (session *Session) enqueue(item outboundFrame) bool {
	if session.outbound == nil {
		return false
	}
	if session.done == nil {
		session.outbound <- item
		return true
	}
	select {
	case session.outbound <- item:
		return true
	case <-session.done:
		return false
	}
}

func (session *Session) close() {
	if session.done != nil {
		session.closeOnce.Do(func() { close(session.done) })
	}
}

func mustHex(value string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(value, " ", ""))
	if err != nil {
		panic(err)
	}
	return b
}

func (s *Server) registerDefaults() {
	// Byte-exact successful replies captured from the final reachable retail
	// services. The redirector hostname resolves locally through the emulator's
	// hosts-file entry and advertises Blaze port 10013 in the captured TDF.
	s.Handle(ComponentRedirector, 1,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			return legacyfire.Reply(req, mustHex(
				"8649320600da1b3503a2fcf4011b6e667368702d7072642d6d702d6170702d30312e65612e636f6d00"+
					"a7000000a998caf913c2fcb4009d9c0100ce58f50001d33dae010100e24bb30000")), nil
		})

	s.Handle(ComponentUtility, 7,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			return legacyfire.Reply(req, mustHex(
				"8e993304001001812019041b1c07090a80e0030f81e00382e00383e0031485e0038efba6038efba605010103"+
					"0b70696e67506572696f6400043135730016766f6970486561647365745570646174655261746500053130303000"+
					"1a786c7370436f6e6e656374696f6e49646c6554696d656f757400043330300000"+
					"c6fcf3038b7c33030000"+
					"b2ec00000ab34c3305010300"+
					"cf6a640085a088d40800cf6972011f426c617a6520332e30352e30312e372028434c232031393033393432290a00")), nil
		})

	s.Handle(ComponentUtility, 2,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			// STIM is Unix time in seconds. Replaying the captured value makes
			// the client's clock-skew checks progressively worse.
			payload := mustHex("cf4a6d00")
			payload = append(payload, blaze.EncodeVarsizeInteger(time.Now().Unix())...)
			return legacyfire.Reply(req, payload), nil
		})

	// PostAuthResponse contains four nested configuration objects. Empty
	// objects keep the generated schema intact while disabling the unavailable
	// retail PSS, telemetry, ticker, and user-options services.
	s.Handle(ComponentUtility, CommandPostAuth,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			return legacyfire.Reply(req, blaze.EncodeTDF(map[string]interface{}{
				"PSS":  map[string]interface{}{},
				"TELE": map[string]interface{}{},
				"TICK": map[string]interface{}{},
				"UROP": map[string]interface{}{},
			})), nil
		})

	// userSettingsLoad must echo the requested key. The retail callback treats
	// only uppercase DATA="TRUE" as enabled; preserve the game's default usage-
	// sharing state while its traffic is directed to the local emulator.
	s.Handle(ComponentUtility, CommandUserSettingsLoad,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			settingKey, _ := req.TDF["KEY"].(string)
			settingValue := ""
			if settingKey == "SHARE_USAGE" {
				settingValue = "TRUE"
			}
			return legacyfire.Reply(req, blaze.EncodeTDF(map[string]interface{}{
				"DATA": settingValue,
				"KEY":  settingKey,
			})), nil
		})

	// setClientMetrics is a one-way state update at the application level. The
	// generated Utility proxy expects a normal, payload-free acknowledgement.
	s.Handle(ComponentUtility, CommandSetMetrics,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			return legacyfire.Reply(req, nil), nil
		})

	// updateNetworkInfo publishes the client's current address/NAT data into
	// UserSessions. A standalone emulator has nothing else to update; the RPC's
	// generated response is void, so acknowledge it without a payload.
	s.Handle(ComponentUserSessions, CommandUpdateNetworkInfo,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			session.mu.Lock()
			if address, ok := networkAddress(req.TDF["ADDR"]); ok {
				session.NetworkAddress = address
			}
			if qos, ok := req.TDF["NQOS"].(map[string]interface{}); ok {
				session.NetworkQOS = qos
			}
			address, qos := session.NetworkAddress, session.NetworkQOS
			session.mu.Unlock()
			s.Logger.Info("network info updated", "remote", session.RemoteAddr,
				"user_session_id", session.userSessionID(), "address", address, "qos", qos)
			return legacyfire.Reply(req, nil), nil
		})

	// updateHardwareFlags has a generated request but no response object.
	s.Handle(ComponentUserSessions, CommandUpdateHardware,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			return legacyfire.Reply(req, nil), nil
		})

	// submitOfflineGameReport uploads queued offline progress (the retail client
	// labels this report type "offlinesync"). Its generated response is void.
	s.Handle(ComponentGameReporting, CommandSubmitOfflineGame,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			return legacyfire.Reply(req, nil), nil
		})

	// HP2010 creates a player-hosted match through resetDedicatedServer rather
	// than createGame. Despite the command name, the Blaze 2 contract accepts a
	// CreateGameRequest and returns JoinGameResponse containing the assigned GID.
	s.Handle(ComponentGameManager, CommandResetDedicated,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			attrs, _ := req.TDF["ATTR"].(map[interface{}]interface{})
			s.mu.Lock()
			if existing := s.games[session.GameID]; existing != nil &&
				sessionForPlayerID(existing, session.PersonaID) == session {
				gameID := existing.ID
				s.mu.Unlock()
				s.Logger.Warn("duplicate game creation ignored", "session_id", session.userSessionID(), "game_id", gameID)
				return legacyfire.Reply(req, blaze.EncodeTDF(map[string]interface{}{"GID": gameID})), nil
			}
			session.GameID = 0
			s.removeQueuedSessionLocked(session)
			gameID := s.nextGameID
			s.nextGameID++
			game := newGame(gameID, session, []*Session{session}, attrs,
				matchmakingCapacity(req), matchmakingTopology(req), 1)
			s.games[gameID] = game
			session.GameID = gameID
			setupPayload := blaze.EncodeTDF(gameSetup(game))
			s.mu.Unlock()
			session.mu.Lock()
			session.Notifications = append(session.Notifications,
				notification(ComponentGameManager, NotifyGameSetup, setupPayload))
			session.mu.Unlock()
			return legacyfire.Reply(req, blaze.EncodeTDF(map[string]interface{}{
				"GID": gameID,
			})), nil
		})

	// finalizeGameCreation publishes the platform-session handles after the
	// client receives NotifyGameSetup. Its generated response is EmptyMessage.
	s.Handle(ComponentGameManager, CommandFinalizeGame,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			gameID, _ := req.TDF["GID"].(int64)
			if gameID == 0 {
				gameID = session.PendingMatchmakingGame
			}
			if gameID == 0 {
				gameID = session.GameID
			}
			if gameID != 0 {
				// Queue this on the requesting session so serveConn writes the RPC
				// reply first. The retail host advances GSTA itself immediately
				// afterwards; finalize only publishes the platform-host handle.
				session.mu.Lock()
				session.Notifications = append(session.Notifications,
					notification(ComponentGameManager, NotifyPlatformHost,
						blaze.EncodeTDF(map[string]interface{}{"GID": gameID, "PHST": int64(0)})))
				session.PendingMatchmakingGame = 0
				session.mu.Unlock()
			}
			return legacyfire.Reply(req, nil), nil
		})

	// advanceGameState is acknowledged with EmptyMessage and broadcast back to
	// game members. The client waits for this notification before adopting the
	// requested state (PRE_GAME during the player-hosted creation sequence).
	s.Handle(ComponentGameManager, CommandAdvanceGameState,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			gameID, _ := req.TDF["GID"].(int64)
			gameState, _ := req.TDF["GSTA"].(int64)
			s.mu.Lock()
			if game := s.games[gameID]; game != nil {
				game.State = gameState
			}
			s.mu.Unlock()
			s.broadcast(session, gameID,
				notification(ComponentGameManager, NotifyGameState,
					blaze.EncodeTDF(map[string]interface{}{
						"GID":  gameID,
						"GSTA": gameState,
					})))
			return legacyfire.Reply(req, nil), nil
		})

	// setPlayerAttributes publishes lobby state such as ready and bounty values.
	// A successful mutation is reflected to members with NotifyPlayerAttribChange.
	s.Handle(ComponentGameManager, CommandSetPlayerAttrs,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			attributes, _ := req.TDF["ATTR"].(map[interface{}]interface{})
			gameID, _ := req.TDF["GID"].(int64)
			playerID, _ := req.TDF["PID"].(int64)
			s.broadcastPlayerAttributes(session, gameID, playerID, attributes)
			return legacyfire.Reply(req, nil), nil
		})

	// setGameAttributes updates lobby-wide configuration. Reflect the accepted
	// values to members through the matching GameManager notification.
	s.Handle(ComponentGameManager, CommandSetGameAttrs,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			attributes, _ := req.TDF["ATTR"].(map[interface{}]interface{})
			gameID, _ := req.TDF["GID"].(int64)
			s.mu.Lock()
			if game := s.games[gameID]; game != nil {
				game.Attrs = attributes
			}
			s.mu.Unlock()
			s.broadcast(session, gameID,
				notification(ComponentGameManager, NotifyGameAttrs,
					blaze.EncodeTDF(map[string]interface{}{
						"ATTR": attributes,
						"GID":  gameID,
					})))
			return legacyfire.Reply(req, nil), nil
		})

	// setGameSettings changes the Blaze game-settings bitmask. The generated
	// notification schema calls this value ATTR even though the request uses GSET.
	s.Handle(ComponentGameManager, CommandSetGameSettings,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			gameID, _ := req.TDF["GID"].(int64)
			settings, _ := req.TDF["GSET"].(int64)
			s.mu.Lock()
			if game := s.games[gameID]; game != nil {
				game.Settings = settings
			}
			s.mu.Unlock()
			s.broadcast(session, gameID,
				notification(ComponentGameManager, NotifyGameSettings,
					blaze.EncodeTDF(map[string]interface{}{
						"ATTR": settings,
						"GID":  gameID,
					})))
			return legacyfire.Reply(req, nil), nil
		})

	// setPlayerCapacity changes the public/private slot counts. The notification
	// schema renames request field PCAP to CAP; TCAP/TEAM may be absent.
	s.Handle(ComponentGameManager, CommandSetPlayerCapacity,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			gameID, _ := req.TDF["GID"].(int64)
			capacities, _ := req.TDF["PCAP"].([]interface{})
			s.mu.Lock()
			if game := s.games[gameID]; game != nil && len(capacities) != 0 {
				if capacity, ok := capacities[0].(int64); ok {
					game.Capacity = capacity
				}
			}
			s.mu.Unlock()
			s.broadcast(session, gameID,
				notification(ComponentGameManager, NotifyGameCapacity,
					blaze.EncodeTDF(map[string]interface{}{
						"CAP": blaze.TypedList{ElemType: 0, Items: capacities},
						"GID": gameID,
					})))
			return legacyfire.Reply(req, nil), nil
		})

	// removePlayer is also used when the local host closes its lobby. Echo the
	// removal context to the client through NotifyPlayerRemoved after the ACK.
	s.Handle(ComponentGameManager, CommandRemovePlayer,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			contextValue, _ := req.TDF["CNTX"].(int64)
			gameID, _ := req.TDF["GID"].(int64)
			playerID, _ := req.TDF["PID"].(int64)
			reason, _ := req.TDF["REAS"].(int64)
			s.mu.Lock()
			game := s.games[gameID]
			var target *Session
			var recipients []*Session
			hostRemoved := false
			if game != nil {
				caller := sessionForPlayerID(game, session.PersonaID)
				candidate := sessionForPlayerID(game, playerID)
				if caller != nil && candidate != nil && (candidate == session || session == game.Host) {
					target = candidate
				}
				if target != nil {
					recipients = append(recipients, game.Members...)
					game.removeMember(target)
					s.removeQueuedSessionLocked(target)
					target.GameID = 0
					target.PendingMatchmakingGame = 0
					if target == game.Host {
						hostRemoved = true
						delete(s.games, gameID)
						for _, member := range game.Members {
							member.GameID = 0
							member.PendingMatchmakingGame = 0
							member.QueuedMatchID = 0
						}
					} else {
						refreshMeshReadiness(game)
					}
				}
			}
			s.mu.Unlock()
			if target == nil {
				if game == nil {
					session.notify(playerRemovedNotification(gameID, playerID, contextValue, reason))
				} else {
					s.Logger.Warn("remove player rejected", "game_id", gameID,
						"from_session_id", session.userSessionID(), "player_id", playerID)
				}
			} else {
				for _, member := range recipients {
					member.notify(playerRemovedNotification(gameID, target.PersonaID, contextValue, reason))
					if hostRemoved && member != target {
						member.notify(gameRemovedNotification(gameID, 2))
					}
				}
			}
			return legacyfire.Reply(req, nil), nil
		})

	// Pair compatible Quick Match requests in a server-wide queue. The first
	// player becomes topology host and both clients receive the same game roster.
	s.Handle(ComponentGameManager, CommandStartMatchmaking,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			matchmakingSessionID := s.queueMatch(session, req)
			return legacyfire.Reply(req, blaze.EncodeTDF(map[string]interface{}{
				"MSID": matchmakingSessionID,
			})), nil
		})

	// updateMeshConnection reports the caller's P2P connectivity to each target.
	// Roster state is already ACTIVE_CONNECTED because HP2010 needs that state to
	// initialize ConnApi; keep actual bidirectional readiness separately.
	s.Handle(ComponentGameManager, CommandUpdateMesh,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			gameID, _ := req.TDF["GID"].(int64)
			targets, _ := req.TDF["TARG"].([]interface{})
			s.updateMeshConnection(session, gameID, targets)
			return legacyfire.Reply(req, nil), nil
		})

	// GetTelemetryServerResponse. DISA=true tells DirtySDK not to open a
	// telemetry socket, while retaining every field in the generated schema.
	s.Handle(ComponentUtility, CommandGetTelemetry,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			return legacyfire.Reply(req, blaze.EncodeTDF(map[string]interface{}{
				"ADRS": "",
				"ANON": false,
				"DISA": true,
				"FILT": "",
				"LOC":  int64(0),
				"NOOK": "",
				"PORT": int64(0),
				"SDLY": int64(0),
				"SKEY": "",
				"SPCT": int64(0),
			})), nil
		})

	// createWalUserSession returns a session key that the client uses to mark the
	// WAL/Autolog login complete before starting the server manifest download.
	s.Handle(ComponentAuthentication, CommandCreateWalSession,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			return legacyfire.Reply(req, blaze.EncodeTDF(map[string]interface{}{
				"KEY": "LOCAL-WAL-SESSION-KEY",
			})), nil
		})

	// getAccount returns the generated Authentication::AccountInfo type used by
	// the retail executable. STAT is EmailStatus::VERIFIED; this field is present
	// in NFS11.exe's serializer even though it is absent from some Blaze 2 SDKs.
	s.Handle(ComponentAuthentication, CommandGetAccount,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			return legacyfire.Reply(req, blaze.EncodeTDF(map[string]interface{}{
				"ASRC": "",
				"CO":   "US",
				"DOB":  "1980-01-01",
				"DTCR": "2010-11-16T00:00:00Z",
				"GOPT": false,
				"LATH": "",
				"LN":   "en",
				"MAIL": "player@localhost",
				"PML":  "",
				"RC":   int64(1), // StatusReason::NONE
				"STAS": int64(1), // AccountStatus::ACTIVE
				"STAT": int64(2), // EmailStatus::VERIFIED
				"TOSV": "",
				"TPOT": false,
				"UID":  session.AccountID,
			})), nil
		})

	// listEntitlements is the final ownership gate before HP2010 creates its Web
	// Access Layer. The request filters for group NFS11HP, so return one active,
	// non-consumable online-access entitlement for the synthetic persona.
	s.Handle(ComponentAuthentication, CommandListEntitlements,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			return legacyfire.Reply(req, blaze.EncodeTDF(map[string]interface{}{
				"NLST": blaze.TypedList{ElemType: 3, Items: []interface{}{
					map[string]interface{}{
						"DEVI": "",
						"GDAY": "2010-11-16T00:00:00Z",
						"GNAM": "NFS11HP",
						"ID":   int64(1),
						"ISCO": false,
						"PID":  session.PersonaID,
						"PJID": "NFS11HP",
						"PRCA": int64(1), // SKUD
						"PRID": "nfs-2011-pc",
						"STAT": int64(1), // ACTIVE
						"STRC": int64(1), // NONE
						"TAG":  "ONLINE_ACCESS",
						"TDAY": "",
						"TYPE": int64(1), // ONLINE_ACCESS
						"UCNT": int64(0),
						"VER":  int64(1),
					},
				}},
			})), nil
		})

	// AssociationLists.getLists returns Lists::LMAP, a list of ListMembers.
	// The local persona begins with no association/friend lists.
	s.Handle(ComponentAssociation, CommandGetLists,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			return legacyfire.Reply(req, blaze.EncodeTDF(map[string]interface{}{
				"LMAP": blaze.TypedList{ElemType: 3, Items: []interface{}{}},
			})), nil
		})

	utilityGame := mustHex("8efba6050101051045787069727954696d657374616d70000b31323839333437323031000a647368744c696d6974000232000e73686966745f747261696c6572000230000a737368744c696d6974000233000b746f735f627566666572000731343030303000")
	s.Handle(ComponentUtility, 1,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			if req.TDF["CFID"] == "Game" {
				return legacyfire.Reply(req, utilityGame), nil
			}
			s.Logger.Info("advertising Autolog HTTP configuration", "base_url", s.AutologBase)
			return legacyfire.Reply(req, blaze.EncodeTDF(map[string]interface{}{
				"CONF": map[interface{}]interface{}{
					"autolog_host":        s.AutologBase,
					"autolog_host_secure": s.AutologBase,
					"content_petition":    "game/post_content_petition.php",
					"content_rep_hide":    "hideContent/[sessionkey]",
					"content_rep_show":    "showContent/[sessionkey]",
					"content_rep_view":    "viewContent/[sessionkey]",
					"event":               "game/post_autolog_event.php",
					"friend_upload":       "game/friend_upload.php",
					"get_photo":           "game/get_photo.php",
					"layout_bundles":      "al/%s/bundles/pc/",
					"lua_bundles":         "al/%s/lua/",
					"page_data":           "al/%s/pages/",
					"photo_space":         "game/photo_space_check.php",
					"post_photo":          "game/post_photo.php",
					"profile_sync":        "game/profile_sync.php",
					"server_manifest":     "al/pc-%s.manifest",
					"view_model":          "_services/json_portal.ws.php",
				},
			})), nil
		})

	// silentLogin returns FullLoginResponse (not LoginResponse). This schema was
	// recovered from the generated Blaze types in NFS11.exe.
	s.Handle(ComponentAuthentication, CommandSilentLogin,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			requestedPersonaID, _ := req.TDF["PID"].(int64)
			s.authenticateSessionAs(session, requestedPersonaID)

			payload := blaze.EncodeTDF(map[string]interface{}{
				"AGUP": false,
				"PCTK": "LOCAL-PC-TOKEN",
				"PRIV": "",
				"SESS": map[string]interface{}{
					"BUID": session.PersonaID,
					"FRST": false,
					"KEY":  "LOCAL-SESSION-KEY",
					"LLOG": int64(0),
					"MAIL": "player@localhost",
					"PDTL": map[string]interface{}{
						"DSNM": session.Persona,
						"LAST": int64(0),
						"PID":  session.PersonaID,
						"XREF": int64(0),
						"XTYP": int64(0),
					},
					"UID": session.AccountID,
				},
				"SPAM": false,
				"THST": "",
				"TURI": "",
			})

			// Blaze normally publishes the authenticated user to the UserSessions
			// component immediately after the login reply.
			extendedData := syntheticUserSessionExtendedData()
			session.Notifications = append(session.Notifications,
				notification(ComponentUserSessions, NotifyUserAdded, blaze.EncodeTDF(map[string]interface{}{
					"DATA": extendedData,
					"USER": map[string]interface{}{
						"AID":  session.AccountID,
						"ALOC": int64(0),
						"EXBB": []byte{},
						"EXID": int64(0),
						"ID":   session.PersonaID,
						"NAME": session.Persona,
					},
				})),
				notification(ComponentUserSessions, NotifyExtendedData, blaze.EncodeTDF(map[string]interface{}{
					"DATA": extendedData,
					// USID is the UserSessionId, not the persona/BlazeId, and must
					// match ReplicatedGamePlayer.UID for this connection.
					"USID": session.userSessionID(),
				})),
			)
			return legacyfire.Reply(req, payload), nil
		})
	s.Handle(ComponentAuthentication, CommandLogout,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			return legacyfire.Reply(req, nil), nil
		})
	s.Handle(ComponentAuthentication, CommandLogin,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			// PNAM="", UID=0: byte-identical to the captured EA rejection payload.
			return legacyfire.ErrorReply(req, AuthInvalidPassword,
				mustHex("c2e86d010100d699000000")), nil
		})

}

func syntheticUserSessionExtendedData() map[string]interface{} {
	return map[string]interface{}{
		"ADDR": blaze.Union{ActiveMember: 0x7f},
		"BPS":  "",
		"CTY":  "US",
		"CVAR": blaze.Variable{Value: nil},
		"HWFG": int64(0),
		"PSLM": blaze.TypedList{ElemType: 0, Items: []interface{}{}},
		"QDAT": map[string]interface{}{
			"DBPS": int64(0),
			"NATT": int64(0),
			"UBPS": int64(0),
		},
		"UATT": int64(0),
		"ULST": blaze.TypedList{ElemType: 9, Items: []interface{}{}},
	}
}

func sessionQOS(session *Session) map[string]interface{} {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.NetworkQOS != nil {
		return session.NetworkQOS
	}
	return map[string]interface{}{
		"DBPS": int64(0),
		"NATT": int64(0),
		"UBPS": int64(0),
	}
}

func networkAddress(value interface{}) (map[string]interface{}, bool) {
	union, ok := value.(blaze.Union)
	if ok {
		address, ok := union.Value.(map[string]interface{})
		return address, ok
	}
	address, ok := value.(map[string]interface{})
	return address, ok
}

func (s *Server) broadcast(sender *Session, gameID int64, frame *legacyfire.Frame) {
	s.mu.Lock()
	game := s.games[gameID]
	var members []*Session
	if game != nil {
		members = append(members, game.Members...)
	}
	s.mu.Unlock()
	if len(members) == 0 {
		sender.notify(frame)
		return
	}
	for _, member := range members {
		member.notify(frame)
	}
}

func (s *Server) broadcastPlayerAttributes(sender *Session, gameID, requestPlayerID int64, attributes map[interface{}]interface{}) {
	s.mu.Lock()
	game := s.games[gameID]
	if game == nil {
		s.mu.Unlock()
		sender.notify(notification(ComponentGameManager, NotifyPlayerAttrs, blaze.EncodeTDF(map[string]interface{}{"ATTR": attributes, "GID": gameID, "PID": requestPlayerID})))
		return
	}
	player := sessionForPlayerID(game, requestPlayerID)
	caller := sessionForPlayerID(game, sender.PersonaID)
	if player == nil || caller == nil || (player != sender && game.Host != sender) {
		s.mu.Unlock()
		s.Logger.Warn("player attribute target rejected", "game_id", gameID,
			"from_session_id", sender.userSessionID(), "player_id", requestPlayerID)
		return
	}
	game.ensureMembershipState()
	if game.PlayerAttrs[player.PersonaID] == nil {
		game.PlayerAttrs[player.PersonaID] = make(map[interface{}]interface{})
	}
	for key, value := range attributes {
		game.PlayerAttrs[player.PersonaID][key] = value
	}
	members := append([]*Session(nil), game.Members...)
	s.mu.Unlock()
	for _, member := range members {
		member.notify(notification(ComponentGameManager, NotifyPlayerAttrs, blaze.EncodeTDF(map[string]interface{}{
			"ATTR": attributes, "GID": gameID, "PID": canonicalPlayerID(player),
		})))
	}
}

func matchmakingCapacity(req *legacyfire.Frame) int64 {
	if caps, ok := req.TDF["PCAP"].([]interface{}); ok && len(caps) != 0 {
		if capacity, ok := caps[0].(int64); ok {
			return capacity
		}
	}
	if capacity, ok := req.TDF["MCAP"].(int64); ok {
		return capacity
	}
	return 8
}

func matchmakingTopology(req *legacyfire.Frame) int64 {
	if topology, ok := req.TDF["NTOP"].(int64); ok {
		// HP2010 requests PEER_TO_PEER_DIRTYCAST_FAILOVER (132), whose
		// ConnApi adapter reserves/removes the topology-host slot for a
		// DirtyCast server. This emulator has no DirtyCast endpoint: assigning
		// that reserved slot to the player host compacts SID 1 onto SID 0, so
		// both peers fail connectToEndpoint with CONNAPI_ERROR_CLIENTLIST (-3).
		// Advertise the direct P2P full-mesh variant instead.
		if topology == 132 {
			return 130
		}
		return topology
	}
	return 0
}

func compatibleMatch(a, b *matchRequest) bool {
	if a.cap < 2 || b.cap < 2 || a.cap != b.cap || a.topology != b.topology {
		return false
	}
	for _, key := range []interface{}{"playlist", "cartier", "event", "visibility"} {
		av, aok := a.attrs[key]
		bv, bok := b.attrs[key]
		if aok != bok || (aok && av != bv) {
			return false
		}
	}
	return true
}

func compatibleGame(game *Game, request *matchRequest) bool {
	if game.Host == request.session || int64(len(game.Members)) >= game.Capacity ||
		game.Capacity != request.cap || game.Topology != request.topology {
		return false
	}
	for _, key := range []interface{}{"playlist", "cartier", "event", "visibility"} {
		requested, hasRequested := request.attrs[key]
		actual, hasActual := game.Attrs[key]
		if hasRequested && (!hasActual || requested != actual) {
			return false
		}
	}
	return true
}

func (s *Server) queueMatch(session *Session, req *legacyfire.Frame) int64 {
	attrs, _ := req.TDF["ATTR"].(map[interface{}]interface{})
	s.mu.Lock()
	msid := s.nextMatchID
	s.nextMatchID++
	if session.GameID != 0 {
		if game := s.games[session.GameID]; game != nil && sessionForPlayerID(game, session.PersonaID) == session {
			s.mu.Unlock()
			s.Logger.Warn("matchmaking request ignored for active game member",
				"session_id", session.userSessionID(), "game_id", session.GameID)
			return msid
		}
		session.GameID = 0
	}
	s.removeQueuedSessionLocked(session)
	candidate := &matchRequest{
		session: session, attrs: attrs, cap: matchmakingCapacity(req),
		topology: matchmakingTopology(req), msid: msid,
	}
	gameIDs := make([]int64, 0, len(s.games))
	for gameID := range s.games {
		gameIDs = append(gameIDs, gameID)
	}
	sort.Slice(gameIDs, func(i, j int) bool { return gameIDs[i] < gameIDs[j] })
	for _, gameID := range gameIDs {
		game := s.games[gameID]
		if !compatibleGame(game, candidate) {
			continue
		}
		if _, added := game.addMember(session); !added {
			continue
		}
		session.GameID = game.ID
		session.PendingMatchmakingGame = game.ID
		session.QueuedMatchID = 0
		members := append([]*Session(nil), game.Members...)
		joiningPlayer := replicatedGamePlayer(game, session)
		// NotifyPlayerJoining is a transitional notification. Advertising an
		// already-connected player here makes the retail client skip/close the
		// ConnApi endpoint; the full setup may still carry the stable roster
		// state used by the lobby view model.
		joiningPlayer["STAT"] = int64(2) // ACTIVE_CONNECTING
		joiningPayload := blaze.EncodeTDF(map[string]interface{}{
			"GID": game.ID, "PDAT": joiningPlayer,
		})
		joinSetup := gameSetup(game)
		joinSetup["REAS"] = matchmakingReason(msid, 1, session.userSessionID())
		joinSetupPayload := blaze.EncodeTDF(joinSetup)
		s.mu.Unlock()
		s.notifyMatchedGame(game, members, candidate, joiningPayload, joinSetupPayload)
		return msid
	}
	matchIndex := -1
	for i, queued := range s.matchQueue {
		if queued.session != session && compatibleMatch(queued, candidate) {
			matchIndex = i
			break
		}
	}
	if matchIndex < 0 {
		s.matchQueue = append(s.matchQueue, candidate)
		session.QueuedMatchID = msid
		s.mu.Unlock()
		return msid
	}
	hostRequest := s.matchQueue[matchIndex]
	s.matchQueue = append(s.matchQueue[:matchIndex], s.matchQueue[matchIndex+1:]...)
	gameID := s.nextGameID
	s.nextGameID++
	game := newGame(gameID, hostRequest.session, []*Session{hostRequest.session, session},
		hostRequest.attrs, hostRequest.cap, hostRequest.topology, 1)
	s.games[gameID] = game
	for _, member := range game.Members {
		member.GameID = gameID
		member.PendingMatchmakingGame = gameID
		member.QueuedMatchID = 0
	}
	hostSetup := gameSetup(game)
	hostSetup["REAS"] = matchmakingReason(hostRequest.msid, 0, hostRequest.session.userSessionID())
	joinSetup := gameSetup(game)
	joinSetup["REAS"] = matchmakingReason(msid, 1, session.userSessionID())
	hostSetupPayload := blaze.EncodeTDF(hostSetup)
	joinSetupPayload := blaze.EncodeTDF(joinSetup)
	s.mu.Unlock()

	for _, frame := range userNotificationsFor(session) {
		hostRequest.session.notify(frame)
	}
	hostRequest.session.enqueue(outboundFrame{frame: notification(ComponentGameManager, NotifyGameSetup, hostSetupPayload), delay: 250 * time.Millisecond})
	// The requesting connection queues its reply after this handler returns. Keep
	// its setup pending so serveConn always puts the RPC reply on the wire first.
	session.mu.Lock()
	session.Notifications = append(session.Notifications, userNotificationsFor(hostRequest.session)...)
	session.Notifications = append(session.Notifications, notification(ComponentGameManager, NotifyGameSetup, joinSetupPayload))
	session.NotificationDelay = 250 * time.Millisecond
	session.mu.Unlock()
	return msid
}

func (s *Server) notifyMatchedGame(game *Game, members []*Session, request *matchRequest, joiningPayload, joinSetupPayload []byte) {
	for _, member := range members {
		s.Logger.Info("game network view", "game_id", game.ID,
			"recipient_player_id", request.session.PersonaID,
			"player_id", member.PersonaID, "user_session_id", member.userSessionID(),
			"name", playerName(member), "slot", game.Slots[member.PersonaID],
			"state", game.PlayerStates[member.PersonaID], "topology_host", member == game.Host,
			"address", sessionIPPair(member), "qos", sessionQOS(member))
	}
	// Existing members already completed game creation. Publish only the joining
	// player's replicated data; a second GameSetup makes the retail host tear its
	// current lobby down.
	for _, member := range members {
		if member == request.session {
			continue
		}
		for _, frame := range userNotificationsFor(request.session) {
			member.notify(frame)
		}
		member.notify(notification(ComponentGameManager, NotifyPlayerJoining, joiningPayload))
	}
	request.session.mu.Lock()
	for _, member := range members {
		if member == request.session {
			continue
		}
		request.session.Notifications = append(request.session.Notifications, userNotificationsFor(member)...)
	}
	request.session.Notifications = append(request.session.Notifications, notification(ComponentGameManager, NotifyGameSetup, joinSetupPayload))
	request.session.NotificationDelay = 250 * time.Millisecond
	request.session.mu.Unlock()
}

func matchmakingReason(msid, result, userID int64) blaze.Union {
	return blaze.Union{ActiveMember: 3, Field: "VALU", Value: map[string]interface{}{
		"FIT": int64(100), "MAXF": int64(100), "MSID": msid, "RSLT": result, "USID": userID,
	}}
}

func sessionIPPair(session *Session) map[string]interface{} {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.NetworkAddress != nil {
		return session.NetworkAddress
	}
	ip := net.ParseIP("127.0.0.1")
	if tcp, ok := session.RemoteAddr.(*net.TCPAddr); ok && tcp.IP != nil {
		ip = tcp.IP
	}
	ip4 := ip.To4()
	numeric := int64(0x7f000001)
	if ip4 != nil {
		numeric = int64(uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3]))
	}
	return map[string]interface{}{
		"EXIP": map[string]interface{}{"IP": int64(0), "PORT": int64(0)},
		"INIP": map[string]interface{}{"IP": numeric, "PORT": int64(3659)},
	}
}

func canonicalPlayerID(player *Session) int64 {
	return player.PersonaID
}

func sessionForPlayerID(game *Game, playerID int64) *Session {
	for _, member := range game.Members {
		if canonicalPlayerID(member) == playerID {
			return member
		}
	}
	return nil
}

func playerName(player *Session) string {
	if player.Persona != "" {
		return player.Persona
	}
	return personaName(player.PersonaID)
}

func userNotificationsFor(player *Session) []*legacyfire.Frame {
	playerID := canonicalPlayerID(player)
	data := syntheticUserSessionExtendedData()
	data["ADDR"] = blaze.Union{ActiveMember: 2, Field: "VALU", Value: sessionIPPair(player)}
	data["QDAT"] = sessionQOS(player)
	return []*legacyfire.Frame{
		notification(ComponentUserSessions, NotifyUserAdded, blaze.EncodeTDF(map[string]interface{}{
			"DATA": data,
			"USER": map[string]interface{}{"AID": player.AccountID, "ALOC": int64(0), "EXBB": []byte{}, "EXID": int64(0), "ID": playerID, "NAME": playerName(player)},
		})),
		// NotifyExtendedData is keyed by UserSessionId. USID must match the
		// global UID carried by ReplicatedGamePlayer.
		notification(ComponentUserSessions, NotifyExtendedData, blaze.EncodeTDF(map[string]interface{}{"DATA": data, "USID": player.userSessionID()})),
	}
}

func (s *Server) updateMeshConnection(session *Session, gameID int64, targets []interface{}) {
	s.mu.Lock()
	game := s.games[gameID]
	if game == nil {
		s.mu.Unlock()
		return
	}
	if sessionForPlayerID(game, session.PersonaID) != session {
		s.mu.Unlock()
		s.Logger.Warn("mesh update from non-member", "game_id", gameID,
			"from_session_id", session.userSessionID())
		return
	}
	game.ensureMembershipState()
	if game.Mesh == nil {
		game.Mesh = make(map[int64]map[int64]int64)
	}
	if game.Mesh[session.PersonaID] == nil {
		game.Mesh[session.PersonaID] = make(map[int64]int64)
	}
	meshChanged := false
	for _, value := range targets {
		target, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		playerID, _ := target["PID"].(int64)
		status, _ := target["STAT"].(int64)
		peer := sessionForPlayerID(game, playerID)
		if peer == nil || peer == session {
			s.Logger.Warn("mesh target does not resolve", "game_id", gameID,
				"from_session_id", session.userSessionID(), "wire_player_id", playerID, "status", status)
			continue
		}
		game.Mesh[session.PersonaID][peer.PersonaID] = status
		meshChanged = true
		s.Logger.Info("mesh connection update", "game_id", gameID,
			"from_session_id", session.userSessionID(), "to_session_id", peer.userSessionID(),
			"wire_player_id", playerID, "status", status)
	}
	var completed []*Session
	var recipients []*Session
	if meshChanged {
		refreshMeshReadiness(game)
		for _, player := range game.Members {
			s.Logger.Info("mesh readiness", "game_id", game.ID,
				"player_id", player.PersonaID, "ready", game.MeshReady[player.PersonaID])
			if game.MeshReady[player.PersonaID] &&
				player.PendingMatchmakingGame == game.ID {
				player.PendingMatchmakingGame = 0
				completed = append(completed, player)
			}
		}
		if len(completed) != 0 {
			recipients = append([]*Session(nil), game.Members...)
		}
	}
	s.mu.Unlock()

	for _, player := range completed {
		for _, recipient := range recipients {
			note := playerJoinCompletedNotification(gameID, player.PersonaID)
			if recipient == session {
				// serveConn enqueues the command-29 reply before flushing these
				// notifications. The retail RPC job must observe its reply first.
				recipient.mu.Lock()
				recipient.Notifications = append(recipient.Notifications, note)
				recipient.mu.Unlock()
				continue
			}
			recipient.notify(note)
		}
		s.Logger.Info("player join completed", "game_id", gameID,
			"player_id", player.PersonaID)
	}
}

func meshReadyForPlayer(game *Game, player *Session) bool {
	connections := game.Mesh[player.PersonaID]
	for _, member := range game.Members {
		if member == player {
			continue
		}
		if connections[member.PersonaID] != 2 || game.Mesh[member.PersonaID][player.PersonaID] != 2 {
			return false
		}
	}
	return true
}

// refreshMeshReadiness maintains the full bidirectional mesh. A pending
// matchmaking player transitions to NotifyPlayerJoinCompleted only after this
// view is ready, matching Blaze's joining -> joined lifecycle.
func refreshMeshReadiness(game *Game) {
	if game.MeshReady == nil {
		game.MeshReady = make(map[int64]bool)
	}
	for _, player := range game.Members {
		game.MeshReady[player.PersonaID] = meshReadyForPlayer(game, player)
	}
}

func gameSetup(registryGame *Game) map[string]interface{} {
	registryGame.ensureMembershipState()
	host := registryGame.Host
	personaID, persona := canonicalPlayerID(host), playerName(host)
	ipPair := sessionIPPair(host)
	hostInfo := map[string]interface{}{
		"HPID": personaID,
		"HSLT": int64(0),
	}

	game := map[string]interface{}{
		"ADMN": blaze.TypedList{ElemType: 0, Items: []interface{}{personaID}},
		"ATTR": registryGame.Attrs,
		"CAP":  blaze.TypedList{ElemType: 0, Items: []interface{}{registryGame.Capacity, int64(0)}},
		"GID":  registryGame.ID,
		"GNAM": persona,
		"GPVH": int64(0),
		"GSET": registryGame.Settings, // invites, join-by-player, host migration, join-in-progress
		"GSID": registryGame.ID,
		"GSTA": registryGame.State,
		"GTYP": "",
		"HNET": blaze.TypedList{ElemType: 3, Items: []interface{}{
			blaze.ArmedStruct{Arm: 2, Fields: ipPair},
		}},
		"HSES": host.userSessionID(),
		"IGNO": false,
		"MCAP": registryGame.Capacity,
		"NQOS": sessionQOS(host),
		// Matchmaking normalizes the retail DirtyCast-failover request to the
		// direct full-mesh topology implemented by this emulator.
		"NTOP": registryGame.Topology,
		"PHST": hostInfo,
		"PSAS": "",
		"QCAP": int64(0),
		"SEED": int64(1),
		"THST": hostInfo,
		"UUID": fmt.Sprintf("LOCAL-GAME-%d", registryGame.ID),
		"VOIP": int64(0),
		"VSTR": "Retail-0.2",
	}

	players := make([]interface{}, 0, len(registryGame.Members))
	admins := []interface{}{personaID}
	for _, member := range registryGame.Members {
		players = append(players, replicatedGamePlayer(registryGame, member))
	}
	game["ADMN"] = blaze.TypedList{ElemType: 0, Items: admins}

	return map[string]interface{}{
		"GAME": game,
		"PROS": blaze.TypedList{ElemType: 3, Items: players},
		"REAS": blaze.Union{
			ActiveMember: 1, // ResetDedicatedServerSetupContext
			Field:        "VALU",
			Value: map[string]interface{}{
				"ERR": int64(0),
			},
		},
	}
}

func replicatedGamePlayer(game *Game, player *Session) map[string]interface{} {
	game.ensureMembershipState()
	playerID := canonicalPlayerID(player)
	displayName := playerName(player)
	playerAttributes := game.PlayerAttrs[player.PersonaID]
	if playerAttributes == nil {
		playerAttributes = map[interface{}]interface{}{}
	}
	return map[string]interface{}{
		"BLOB": []byte{}, "EXID": int64(0), "GID": game.ID, "LOC": int64(0),
		"NAME": displayName, "PID": playerID,
		"NQOS": sessionQOS(player),
		"PATT": playerAttributes,
		"PNET": blaze.Union{ActiveMember: 2, Field: "VALU", Value: sessionIPPair(player)},
		// SID is the roster index; SLOT is the SlotType enum. Both players are
		// public participants (0), occupying SID 0 and SID 1 respectively.
		"SID": game.Slots[player.PersonaID], "SLOT": int64(0), "STAT": game.PlayerStates[player.PersonaID],
		"TEAM": int64(-1), "TIDX": int64(-1), "TIME": game.JoinedAt[player.PersonaID],
		"UGID": blaze.ObjectID{}, "UID": player.userSessionID(),
	}
}

func notification(component, command uint16, payload []byte) *legacyfire.Frame {
	return &legacyfire.Frame{
		Header: legacyfire.Header{
			Component:   component,
			Command:     command,
			MessageType: legacyfire.MessageNotify,
		},
		Payload: payload,
	}
}
