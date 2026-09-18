package hpserver

import (
	"context"
	"encoding/hex"
	"strings"
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
	ComponentAutolog        = 0x0801
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
	CommandGetBestScores     = 0x0b
	CommandSubmitGameReport  = 0x01
	CommandSubmitOfflineGame = 0x02
	CommandAdvanceGameState  = 0x03
	CommandSetGameSettings   = 0x04
	CommandSetPlayerCapacity = 0x05
	CommandSetGameAttrs      = 0x07
	CommandSetPlayerAttrs    = 0x08
	CommandRemovePlayer      = 0x0b
	CommandStartMatchmaking  = 0x0d
	CommandCancelMatchmaking = 0x0e
	CommandFinalizeGame      = 0x0f
	CommandReplayGame        = 0x13
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

func networkAddress(value interface{}) (map[string]interface{}, bool) {

	union, ok := value.(blaze.Union)
	if ok {
		address, ok := union.Value.(map[string]interface{})
		return address, ok
	}

	address, ok := value.(map[string]interface{})
	return address, ok

}

func mustHex(value string) []byte {

	b, err := hex.DecodeString(strings.ReplaceAll(value, " ", ""))
	if err != nil {
		panic(err)
	}

	return b

}

func (s *Server) registerDefaultHandlers() {

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

	// submitGameReport uploads the final online match report. The generated SDK
	// has SubmitGameReportRequest but no response object, so acknowledge it with
	// an empty reply after recording the report identity for diagnostics.
	s.Handle(ComponentGameReporting, CommandSubmitGameReport,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			finished, _ := req.TDF["FNSH"].(int64)
			report, _ := req.TDF["RPRT"].(map[string]interface{})
			gameReportingID, _ := report["GRID"].(int64)
			gameType, _ := report["GTYP"].(string)
			s.Logger.Info("online game report submitted",
				"session_id", session.userSessionID(), "finished", finished,
				"game_reporting_id", gameReportingID, "game_type", gameType)
			return legacyfire.Reply(req, nil), nil
		})

	// submitOfflineGameReport uploads queued offline progress (the retail client
	// labels this report type "offlinesync"). Its generated response is void.
	s.Handle(ComponentGameReporting, CommandSubmitOfflineGame,
		func(_ context.Context, _ *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			return legacyfire.Reply(req, nil), nil
		})

	// HP2010's title-specific AutologComponent shares component ID 0x0801
	// with RspComponent in later Blaze SDKs. getBestScores batches the saved
	// event requests in REQS and expects BestScoresResponse, whose SCRS member
	// is a list<BestScoreRow>. With no persisted speedwall data, return the
	// schema-correct empty list so profile synchronization can continue.
	s.Handle(ComponentAutolog, CommandGetBestScores,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			blazeID, _ := req.TDF["BLID"].(int64)
			requests, _ := req.TDF["REQS"].([]interface{})
			s.Logger.Info("Autolog best scores requested",
				"session_id", session.userSessionID(), "blaze_id", blazeID,
				"request_count", len(requests))
			return legacyfire.Reply(req, blaze.EncodeTDF(map[string]interface{}{
				"SCRS": blaze.TypedList{ElemType: 3, Items: []interface{}{}},
			})), nil
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
				getMatchmakingCapacity(req), getMatchmakingTopology(req), 1)
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

	// cancelMatchmaking carries only MSID and has a void response. Cancellation
	// applies exclusively to the caller's still-queued request; once matchmaking
	// has produced a game, normal GameManager removal owns that lifecycle.
	s.Handle(ComponentGameManager, CommandCancelMatchmaking,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			matchmakingSessionID, _ := req.TDF["MSID"].(int64)
			cancelled := s.cancelQueuedMatch(session, matchmakingSessionID)
			s.Logger.Info("matchmaking cancelled",
				"session_id", session.userSessionID(),
				"matchmaking_session_id", matchmakingSessionID,
				"removed", cancelled)
			return legacyfire.Reply(req, nil), nil
		})

	// replayGame retains the current roster after a completed match and returns
	// it to PRE_GAME. The generated response is EmptyMessage; clients adopt the
	// transition from NotifyGameStateChange before preparing the next round.
	s.Handle(ComponentGameManager, CommandReplayGame,
		func(_ context.Context, session *Session, req *legacyfire.Frame) (*legacyfire.Frame, error) {
			gameID, _ := req.TDF["GID"].(int64)
			s.mu.Lock()
			game := s.games[gameID]
			accepted := game != nil && sessionForPlayerID(game, session.PersonaID) == session && game.Host == session
			if accepted {
				game.State = 130
			}
			s.mu.Unlock()
			if accepted {
				s.broadcast(session, gameID,
					notification(ComponentGameManager, NotifyGameState,
						blaze.EncodeTDF(map[string]interface{}{
							"GID":  gameID,
							"GSTA": int64(130),
						})))
			}
			s.Logger.Info("game replay requested",
				"session_id", session.userSessionID(), "game_id", gameID, "accepted", accepted)
			return legacyfire.Reply(req, nil), nil
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
			personaToken, _ := req.TDF["PCTK"].(string)
			s.authenticateSessionNamed(session, requestedPersonaID, personaNameFromToken(personaToken))

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
