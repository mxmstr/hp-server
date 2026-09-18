package hpserver

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"sort"
	"time"

	blaze "github.com/local/reorigin-hotpursuit"
	"github.com/local/reorigin-hotpursuit/legacyfire"
)

func getMatchmakingCapacity(req *legacyfire.Frame) int64 {

	if caps, ok := req.TDF["PCAP"].([]interface{}); ok && len(caps) != 0 {
		if capacity, ok := caps[0].(int64); ok {
			return capacity
		}
	}

	if capacity, ok := req.TDF["PMAX"].(int64); ok && capacity > 0 {
		return capacity
	}

	if capacity, ok := req.TDF["MCAP"].(int64); ok {
		return capacity
	}

	return 8

}

func getMatchmakingTopology(req *legacyfire.Frame) int64 {

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

func isCompatibleMatch(a, b *matchRequest) bool {

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

func getMatchmakingReason(msid, result, userID int64) blaze.Union {

	return blaze.Union{ActiveMember: 3, Field: "VALU", Value: map[string]interface{}{
		"FIT": int64(100), "MAXF": int64(100), "MSID": msid, "RSLT": result, "USID": userID,
	}}

}

func (s *Server) newSession(remote net.Addr) *Session {
	return &Session{RemoteAddr: remote, outbound: make(chan outboundFrame, 256), done: make(chan struct{})}
}

func (s *Server) authenticateSessionNamed(session *Session, requestedPersonaID int64, requestedName string) {

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
	}

	if requestedName != "" {
		identity.Name = requestedName
	}

	s.users[personaID] = identity
	if personaID >= s.nextUserID {
		s.nextUserID = personaID + 1
	}

	session.AccountID = identity.AccountID
	session.PersonaID = identity.PersonaID
	session.SessionID = s.nextSessionID
	session.Persona = identity.Name
	s.nextSessionID++

	s.sessions[session.SessionID] = session
	s.activePersonas[session.PersonaID] = session

}

func (s *Server) authenticateSessionAs(session *Session, requestedPersonaID int64) {
	s.authenticateSessionNamed(session, requestedPersonaID, "")
}

func (s *Server) authenticateSession(session *Session) {
	s.authenticateSessionAs(session, 0)
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

func (s *Server) broadcast(sender *Session, gameID int64, frame *legacyfire.Frame) {

	s.mu.Lock()

	var members []*Session

	game := s.games[gameID]
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
		member.notify(notification(
			ComponentGameManager,
			NotifyPlayerAttrs,
			blaze.EncodeTDF(map[string]interface{}{
				"ATTR": attributes, "GID": gameID, "PID": canonicalPlayerID(player),
			})))
	}

}

func (s *Server) queueMatch(session *Session, req *legacyfire.Frame) int64 {

	s.mu.Lock()

	attrs, _ := req.TDF["ATTR"].(map[interface{}]interface{})
	msid := s.nextMatchID
	s.nextMatchID++

	if session.GameID != 0 {

		if game := s.games[session.GameID]; game != nil &&
			sessionForPlayerID(game, session.PersonaID) == session {
			s.mu.Unlock()
			s.Logger.Warn("matchmaking request ignored for active game member",
				"session_id", session.userSessionID(), "game_id", session.GameID)
			return msid
		}

		session.GameID = 0

	}

	s.removeQueuedSessionLocked(session)

	candidate := &matchRequest{
		session:  session,
		attrs:    attrs,
		cap:      getMatchmakingCapacity(req),
		topology: getMatchmakingTopology(req),
		msid:     msid,
	}

	gameIDs := make([]int64, 0, len(s.games))
	for gameID := range s.games {
		gameIDs = append(gameIDs, gameID)
	}

	sort.Slice(gameIDs, func(i, j int) bool { return gameIDs[i] < gameIDs[j] })

	for _, gameID := range gameIDs {

		game := s.games[gameID]

		if !isCompatibleGame(game, candidate) {
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
		joinSetup["REAS"] = getMatchmakingReason(msid, 1, session.userSessionID())
		joinSetupPayload := blaze.EncodeTDF(joinSetup)

		s.mu.Unlock()
		s.notifyMatchedGame(game, members, candidate, joiningPayload, joinSetupPayload)

		return msid

	}

	matchIndex := -1
	for i, queued := range s.matchQueue {
		if queued.session != session && isCompatibleMatch(queued, candidate) {
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
	hostSetup["REAS"] = getMatchmakingReason(hostRequest.msid, 0, hostRequest.session.userSessionID())
	joinSetup := gameSetup(game)
	joinSetup["REAS"] = getMatchmakingReason(msid, 1, session.userSessionID())
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

func (s *Server) cancelQueuedMatch(session *Session, matchmakingSessionID int64) bool {

	if session == nil || matchmakingSessionID <= 0 {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if session.QueuedMatchID != matchmakingSessionID {
		return false
	}

	for i, queued := range s.matchQueue {

		if queued.session != session || queued.msid != matchmakingSessionID {
			continue
		}

		s.matchQueue = append(s.matchQueue[:i], s.matchQueue[i+1:]...)
		session.QueuedMatchID = 0

		return true

	}

	return false

}

func (s *Server) notifyMatchedGame(
	game *Game,
	members []*Session,
	request *matchRequest,
	joiningPayload, joinSetupPayload []byte) {

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

func key(component, command uint16) uint32 { return uint32(component)<<16 | uint32(command) }

func (s *Server) Handle(component, command uint16, handler Handler) {
	s.Handlers[key(component, command)] = handler
}

func New(logger *slog.Logger) *Server {

	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		Logger:         logger,
		Handlers:       make(map[uint32]Handler),
		AutologBase:    "http://127.0.0.1:18080",
		nextUserID:     1,
		nextSessionID:  1,
		nextGameID:     1,
		nextMatchID:    1,
		users:          make(map[int64]UserIdentity),
		sessions:       make(map[int64]*Session),
		activePersonas: make(map[int64]*Session),
		games:          make(map[int64]*Game)}
	s.registerDefaultHandlers()

	return s

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
